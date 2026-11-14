package events_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// offsetReader serves msgs in order and records every committed offset.
type offsetReader struct {
	mu        sync.Mutex
	msgs      []kafka.Message
	i         int
	committed []int64
}

func (r *offsetReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	if r.i < len(r.msgs) {
		m := r.msgs[r.i]
		r.i++
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

func (r *offsetReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.committed = append(r.committed, m.Offset)
	}
	return nil
}

func (r *offsetReader) offsets() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.committed...)
}

// failFirstWriter fails its first write only: the first dead-letter write
// fails, the second message's would succeed.
type failFirstWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *failFirstWriter) WriteMessages(context.Context, ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls == 1 {
		return errors.New("dlq broker unavailable")
	}
	return nil
}

func offsetMessage(t *testing.T, offset int64) kafka.Message {
	t.Helper()
	payload, err := protojson.Marshal(&usersv1.UserRegistered{EventId: "e", UserId: "u"})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{
		Topic: "user.events", Partition: 0, Offset: offset, Value: payload,
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
	}
}

// SL-00 / SL-16: the single-message failure-path tests cannot see the loss.
// kafka-go commits a partition by position (offset+1): when message 0's
// dead-letter write failed, the release 1 dispatcher logged "leaving offset
// uncommitted" and moved on, and the NEXT successful commit — message 1's —
// committed past message 0. Message 0 was gone, with lag 0 and no counter
// moving (on that tree this test failed with "Should be empty, but was [1]").
//
// Since release 2 (owner ruling 13.11.2026, reversing go-common#38) the
// dispatcher STOPS: it commits nothing, fetches nothing more, and Run returns an
// error that makes the process exit non-zero.
func TestDispatcher_FailedDeadLetterWriteStopsTheConsumerAndCommitsNothing(t *testing.T) {
	reader := &offsetReader{msgs: []kafka.Message{offsetMessage(t, 0), offsetMessage(t, 1)}}
	// Message 0's handler fails and so does its dead-letter write; message 1's
	// handler would succeed — if it were ever fetched.
	var handled []int
	disp := newDisp(reader, &failFirstWriter{}, events.WithRetryWindow(time.Millisecond))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		handled = append(handled, len(handled))
		if len(handled) == 1 {
			return errors.New("transient")
		}
		return nil
	})

	// A live context: Run must stop by itself, not because of a cancel.
	err := runToReturn(t, disp)

	require.ErrorIs(t, err, events.ErrDeadLetterWriteFailed)
	var ae *commonerrors.AppError
	require.True(t, errors.As(err, &ae), "a typed error, got %T", err)
	assert.Equal(t, events.ReasonDeadLetterWriteFailed, ae.Reason)
	assert.Empty(t, reader.offsets(), "nothing may be committed once message 0's dead-letter write failed")
	reader.mu.Lock()
	fetched := reader.i
	reader.mu.Unlock()
	assert.Equal(t, 1, fetched, "message 1 is never fetched: its commit would consume message 0")
	assert.Len(t, handled, 1)

	// The stop is permanent.
	again := disp.Run(context.Background())
	require.ErrorIs(t, again, events.ErrDeadLetterWriteFailed)
	reader.mu.Lock()
	assert.Equal(t, 1, reader.i, "a second Run fetches nothing")
	reader.mu.Unlock()
}

// The legacy retry tier's write failing is the same fault.
func TestDispatcher_FailedRetryTierWriteAlsoStops(t *testing.T) {
	reader := &offsetReader{msgs: []kafka.Message{offsetMessage(t, 0), offsetMessage(t, 1)}}
	disp := newDisp(reader, &fakeWriter{}, events.WithRetry(&failFirstWriter{}))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return errors.New("transient") })

	err := runToReturn(t, disp)

	require.ErrorIs(t, err, events.ErrDeadLetterWriteFailed)
	assert.Empty(t, reader.offsets())
}

// Worker is what makes the stop LOUD: the error reaches pkg/lifecycle, which
// exits the process non-zero, whatever the state of the shutdown.
func TestDispatcherWorker_MapsTheStopsForLifecycle(t *testing.T) {
	deadLetterFails := func() *events.Dispatcher {
		r := &offsetReader{msgs: []kafka.Message{offsetMessage(t, 0)}}
		d := newDisp(r, &failFirstWriter{}, events.WithRetryWindow(time.Millisecond))
		events.Handle(d, func(context.Context, *usersv1.UserRegistered) error { return errors.New("x") })
		return d
	}
	leaves := func() *events.Dispatcher {
		r := &offsetReader{msgs: []kafka.Message{offsetMessage(t, 0)}}
		d := newDisp(r, &fakeWriter{})
		events.Handle(d, func(context.Context, *usersv1.UserRegistered) error { return leaveAtShutdown() })
		return d
	}
	running := make(chan struct{})
	stopping := make(chan struct{})
	close(stopping)

	t.Run("a failed dead-letter write is an error even while the process is running", func(t *testing.T) {
		err := deadLetterFails().Worker(running)(context.Background())
		require.ErrorIs(t, err, events.ErrDeadLetterWriteFailed)
	})
	t.Run("and even during shutdown", func(t *testing.T) {
		err := deadLetterFails().Worker(stopping)(context.Background())
		require.ErrorIs(t, err, events.ErrDeadLetterWriteFailed)
	})
	t.Run("a handler that left a message is an error while running", func(t *testing.T) {
		err := leaves().Worker(running)(context.Background())
		require.ErrorIs(t, err, events.ErrLeaveUncommitted)
	})
	t.Run("and a clean stop once the process is stopping", func(t *testing.T) {
		assert.NoError(t, leaves().Worker(stopping)(context.Background()))
	})
	t.Run("a cancelled context is the cancel", func(t *testing.T) {
		d := newDisp(&offsetReader{}, &fakeWriter{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.ErrorIs(t, d.Worker(running)(ctx), context.Canceled)
	})
}

// R11: Run returns nil on a closed reader, which Worker used to pass on as a
// clean return; lifecycle then kept the pod alive around a dead consumer.
func TestDispatcherWorker_AReaderClosedWhileRunningIsAnError(t *testing.T) {
	closedReader := func() *events.Dispatcher {
		return newDisp(&scriptedFetchReader{script: []fetchStep{{err: io.EOF}}}, &fakeWriter{})
	}
	running := make(chan struct{})
	stopping := make(chan struct{})
	close(stopping)

	err := closedReader().Worker(running)(context.Background())
	require.ErrorIs(t, err, events.ErrReaderClosed)
	var ae *commonerrors.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, events.ReasonReaderClosed, ae.Reason)

	assert.NoError(t, closedReader().Worker(stopping)(context.Background()), "closed during shutdown is the shutdown")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.NotErrorIs(t, closedReader().Worker(running)(ctx), events.ErrReaderClosed, "a cancelled context is the cancel")
}
