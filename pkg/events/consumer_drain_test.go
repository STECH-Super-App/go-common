package events_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// These tests pin the shutdown contract of Run: cancelling its ctx stops the
// NEXT fetch, but the message already fetched is handled, deduplicated,
// dead-lettered and committed on a live context. A cancel that reached those
// steps would let the handler's side effect happen and then fail the steps
// that record it, so Kafka would redeliver and the side effect would repeat.

// greedyReader models kafka-go's worst case: while messages are buffered it
// hands one out WITHOUT looking at ctx (kafka-go picks at random between a
// buffered message and ctx.Done). Once drained it blocks on ctx like an idle
// reader. Every fetch and every commit's context state is recorded.
type greedyReader struct {
	mu         sync.Mutex
	msgs       []kafka.Message
	fetches    int
	commitErrs []error
}

func (r *greedyReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	r.fetches++
	if len(r.msgs) > 0 {
		m := r.msgs[0]
		r.msgs = r.msgs[1:]
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

// CommitMessages fails on a cancelled context, as kafka-go's does.
func (r *greedyReader) CommitMessages(ctx context.Context, _ ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitErrs = append(r.commitErrs, ctx.Err())
	return ctx.Err()
}

func (r *greedyReader) snapshot() (fetches int, commitErrs []error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetches, append([]error(nil), r.commitErrs...)
}

// ctxCheckingWriter fails a write on a cancelled context, as kafka-go's does,
// and records the context state of every attempt.
type ctxCheckingWriter struct {
	mu   sync.Mutex
	errs []error
	msgs []kafka.Message
}

func (w *ctxCheckingWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errs = append(w.errs, ctx.Err())
	if err := ctx.Err(); err != nil {
		return err
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *ctxCheckingWriter) snapshot() (errs []error, written int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]error(nil), w.errs...), len(w.msgs)
}

// txDedup models outbox.Deduplicator: fn runs inside a transaction whose
// COMMIT happens after fn on the same ctx, so a ctx cancelled while fn ran
// rolls the event_id claim back.
type txDedup struct {
	mu        sync.Mutex
	commitErr error
	calls     int
}

func (d *txDedup) Process(ctx context.Context, _ string, fn func() error) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.commitErr = ctx.Err()
	return d.commitErr
}

// blockingHandler parks until released and records the context state it saw
// at the moment it finished — i.e. after the cancel.
type blockingHandler struct {
	entered chan struct{}
	release chan struct{}
	result  error

	mu       sync.Mutex
	calls    int
	ctxErrAt error
	ctxValue any
}

func newBlockingHandler(result error) *blockingHandler {
	return &blockingHandler{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
		result:  result,
	}
}

type drainCtxKey struct{}

func (h *blockingHandler) handle(ctx context.Context, _ *usersv1.UserRegistered) error {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	h.entered <- struct{}{}
	<-h.release
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctxErrAt = ctx.Err()
	h.ctxValue = ctx.Value(drainCtxKey{})
	if h.ctxErrAt != nil {
		// A real handler's DB/HTTP call would fail on a cancelled ctx.
		return h.ctxErrAt
	}
	return h.result
}

func (h *blockingHandler) state() (calls int, value any, ctxErr error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls, h.ctxValue, h.ctxErrAt
}

func userRegisteredMsg(t *testing.T, eventID string) kafka.Message {
	t.Helper()
	payload, err := protojson.Marshal(&usersv1.UserRegistered{EventId: eventID, UserId: "u1", Name: "Alice"})
	require.NoError(t, err)
	return kafka.Message{
		Topic: "user.events",
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   eventID,
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
}

// runUntilCancelMidHandler starts Run, cancels its ctx while the first
// message is inside the handler, checks Run does not return while that
// message is in flight, releases the handler and returns Run's result.
func runUntilCancelMidHandler(t *testing.T, disp *events.Dispatcher, h *blockingHandler) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), drainCtxKey{}, "kept"))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	select {
	case <-h.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first message never reached the handler")
	}
	cancel() // shutdown begins while the first message is mid-handler
	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) while a message was still in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(h.release)

	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the in-flight message finished")
		return nil
	}
}

// The re-send guard: the handler, the dedup commit and the offset commit of
// the message in flight at shutdown all see a live context (with Run's values
// kept), and the second message — already buffered — is neither fetched nor
// handled.
func TestDispatcherRun_ShutdownFinishesInFlightMessage(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{
		userRegisteredMsg(t, "e1"), userRegisteredMsg(t, "e2"),
	}}
	dlq := &ctxCheckingWriter{}
	dedup := &txDedup{}
	h := newBlockingHandler(nil)

	disp := events.NewDispatcher(reader, dlq, events.WithDedup(dedup))
	events.Handle(disp, h.handle)

	err := runUntilCancelMidHandler(t, disp, h)
	require.ErrorIs(t, err, context.Canceled)

	calls, value, ctxErr := h.state()
	assert.NoError(t, ctxErr, "the handler's context was cancelled mid-message")
	assert.Equal(t, "kept", value, "the handler context must keep Run's values")
	assert.Equal(t, 1, calls, "no message may be handled after shutdown began")

	dedup.mu.Lock()
	assert.NoError(t, dedup.commitErr, "the dedup transaction committed on a cancelled context")
	assert.Equal(t, 1, dedup.calls)
	dedup.mu.Unlock()

	fetches, commitErrs := reader.snapshot()
	assert.Equal(t, 1, fetches, "the buffered second message must not be fetched after shutdown")
	require.Len(t, commitErrs, 1, "exactly the in-flight message's offset is committed")
	assert.NoError(t, commitErrs[0], "the offset commit ran on a cancelled context")

	errs, written := dlq.snapshot()
	assert.Empty(t, errs, "a handled message must not reach the DLQ")
	assert.Zero(t, written)
}

// A handler that fails while shutdown is under way is still dead-lettered —
// on a live context — and its offset committed, rather than the DLQ write
// failing on the cancelled ctx and the message being redelivered.
func TestDispatcherRun_ShutdownStillDeadLettersInFlightFailure(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{
		userRegisteredMsg(t, "e1"), userRegisteredMsg(t, "e2"),
	}}
	dlq := &ctxCheckingWriter{}
	h := newBlockingHandler(fmt.Errorf("bad payload: %w", events.ErrPoisonPill))

	disp := events.NewDispatcher(reader, dlq)
	events.Handle(disp, h.handle)

	err := runUntilCancelMidHandler(t, disp, h)
	require.ErrorIs(t, err, context.Canceled)

	errs, written := dlq.snapshot()
	require.Len(t, errs, 1, "the failed message must be dead-lettered exactly once")
	assert.NoError(t, errs[0], "the DLQ write ran on a cancelled context")
	assert.Equal(t, 1, written)

	fetches, commitErrs := reader.snapshot()
	assert.Equal(t, 1, fetches)
	require.Len(t, commitErrs, 1)
	assert.NoError(t, commitErrs[0], "the offset commit after the DLQ write ran on a cancelled context")
}

// The same holds for the retry tier: a retryable failure in flight at
// shutdown is forwarded on a live context instead of being redelivered.
func TestDispatcherRun_ShutdownStillForwardsInFlightRetry(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	dlq := &ctxCheckingWriter{}
	retry := &ctxCheckingWriter{}
	h := newBlockingHandler(errors.New("transient"))

	disp := events.NewDispatcher(reader, dlq, events.WithRetry(retry))
	events.Handle(disp, h.handle)

	err := runUntilCancelMidHandler(t, disp, h)
	require.ErrorIs(t, err, context.Canceled)

	errs, written := retry.snapshot()
	require.Len(t, errs, 1)
	assert.NoError(t, errs[0], "the retry write ran on a cancelled context")
	assert.Equal(t, 1, written)
	dlqErrs, _ := dlq.snapshot()
	assert.Empty(t, dlqErrs)
}

// With nothing in flight, cancel alone ends the blocking fetch.
func TestDispatcherRun_IdleShutdownReturnsPromptly(t *testing.T) {
	reader := &greedyReader{}
	disp := events.NewDispatcher(reader, &ctxCheckingWriter{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	require.Eventually(t, func() bool { f, _ := reader.snapshot(); return f == 1 },
		2*time.Second, 5*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("idle Run did not return on cancel")
	}
}

// A ctx already cancelled before Run starts fetches nothing, even with a
// message buffered.
func TestDispatcherRun_CancelledBeforeStartFetchesNothing(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	h := newBlockingHandler(nil)
	close(h.release) // a regression must fail this test, not hang it
	disp := events.NewDispatcher(reader, &ctxCheckingWriter{})
	events.Handle(disp, h.handle)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, disp.Run(ctx), context.Canceled)

	fetches, _ := reader.snapshot()
	assert.Zero(t, fetches)
	calls, _, _ := h.state()
	assert.Zero(t, calls)
}
