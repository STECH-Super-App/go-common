package events_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
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

// SL-00 / SL-16: the existing failure-path tests feed ONE message, so they
// cannot see the loss. kafka-go commits a partition by position (offset+1):
// when message 0's dead-letter write fails, the dispatcher logs "leaving offset
// uncommitted" and moves on, and the NEXT successful commit — message 1's —
// commits past message 0. Message 0 is gone, with lag 0 and no counter moving.
//
// Known-red until release 2 (stop on a failed dead-letter write, owner ruling
// 13.11.2026). Off unless GOCOMMON_KNOWN_RED=1 so CI stays green; run on the
// 14.11.2026 tree it FAILS, which is the evidence for the finding.
func TestDispatcher_KnownRed_FailedDeadLetterWriteMustNotBeCommittedPast(t *testing.T) {
	if os.Getenv("GOCOMMON_KNOWN_RED") != "1" {
		t.Skip("SL-00 is fixed in go-common release 2; set GOCOMMON_KNOWN_RED=1 to see it fail")
	}

	reader := &offsetReader{msgs: []kafka.Message{offsetMessage(t, 0), offsetMessage(t, 1)}}
	// Message 0's handler fails and so does its dead-letter write; message 1's
	// handler succeeds.
	var n int
	disp := events.NewDispatcher(reader, &failFirstWriter{})
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		n++
		if n == 1 {
			return errors.New("transient")
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	assert.Empty(t, reader.offsets(),
		"nothing may be committed once message 0's dead-letter write failed: any later commit marks message 0 consumed")
}
