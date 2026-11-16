package events_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// --- test doubles ---

type fakeReader struct {
	msgs []kafka.Message
	i    int
	mu   sync.Mutex
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.i >= len(r.msgs) {
		<-ctx.Done()
		return kafka.Message{}, ctx.Err()
	}
	m := r.msgs[r.i]
	r.i++
	return m, nil
}

func (r *fakeReader) CommitMessages(_ context.Context, _ ...kafka.Message) error {
	return nil
}

type fakeWriter struct {
	mu   sync.Mutex
	msgs []kafka.Message
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *fakeWriter) captured() []kafka.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]kafka.Message, len(w.msgs))
	copy(out, w.msgs)
	return out
}

func kafkaHeaders(h map[string]string) []kafka.Header {
	out := make([]kafka.Header, 0, len(h))
	for k, v := range h {
		out = append(out, kafka.Header{Key: k, Value: []byte(v)})
	}
	return out
}

// --- tests ---

func TestDispatcher_routesToHandler(t *testing.T) {
	payload, err := protojson.Marshal(&usersv1.UserRegistered{
		EventId: "e1", UserId: "u1", Name: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	var gotName string
	handlerCh := make(chan struct{}, 1)

	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, e *usersv1.UserRegistered) error {
		gotName = e.Name
		handlerCh <- struct{}{}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = disp.Run(ctx) }()

	select {
	case <-handlerCh:
	case <-ctx.Done():
		t.Fatal("handler never invoked")
	}
	if gotName != "Alice" {
		t.Errorf("handler got name %q, want %q", gotName, "Alice")
	}
	if len(dlq.captured()) != 0 {
		t.Errorf("DLQ got %d unexpected messages", len(dlq.captured()))
	}
}

func TestDispatcher_unknownEventType_skipsNoDLQ(t *testing.T) {
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.NotARealEvent",
		}),
		Value: []byte(`{}`),
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	invoked := false
	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		invoked = true
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	if invoked {
		t.Error("handler invoked for unknown event type")
	}
	if got := len(dlq.captured()); got != 0 {
		t.Errorf("DLQ got %d messages for unknown event, want 0", got)
	}
}

// --- helpers for failure-path tests ---

func findTestHeader(m kafka.Message, key string) []byte {
	for _, h := range m.Headers {
		if h.Key == key {
			return h.Value
		}
	}
	return nil
}

// --- B6: poison pill, unmarshal error, handler panic all go to DLQ ---

func TestDispatcher_poisonPill_goesStraightToDLQ(t *testing.T) {
	payload, _ := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		return fmt.Errorf("bad: %w", events.ErrPoisonPill)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	caps := dlq.captured()
	if len(caps) != 1 {
		t.Fatalf("DLQ got %d messages, want 1", len(caps))
	}
	if got := string(findTestHeader(caps[0], "x-dlq-reason")); got != "poison_pill" {
		t.Errorf("x-dlq-reason = %q, want %q", got, "poison_pill")
	}
}

func TestDispatcher_unmarshalError_goesToDLQ(t *testing.T) {
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: []byte(`{not valid json`),
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		t.Fatal("handler must not be invoked for unmarshal failures")
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	caps := dlq.captured()
	if len(caps) != 1 {
		t.Fatalf("DLQ got %d messages, want 1", len(caps))
	}
	if got := string(findTestHeader(caps[0], "x-dlq-reason")); got != "unmarshal_error" {
		t.Errorf("x-dlq-reason = %q, want %q", got, "unmarshal_error")
	}
}

func TestDispatcher_handlerPanic_goesToDLQ(t *testing.T) {
	payload, _ := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		panic("boom")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	caps := dlq.captured()
	if len(caps) != 1 {
		t.Fatalf("DLQ got %d messages, want 1", len(caps))
	}
	if got := string(findTestHeader(caps[0], "x-dlq-reason")); got != "handler_panic" {
		t.Errorf("x-dlq-reason = %q, want %q", got, "handler_panic")
	}
}

// --- B7: max retries -> DLQ with reason=max_retries ---

func TestDispatcher_maxRetries_goesToDLQ(t *testing.T) {
	payload, _ := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:    "e1",
			envelope.HeaderEventType:  "events.users.v1.UserRegistered",
			envelope.HeaderRetryCount: "3",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}

	disp := newDisp(reader, dlq, events.WithMaxRetries(3))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		return errors.New("transient failure")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	caps := dlq.captured()
	if len(caps) != 1 {
		t.Fatalf("DLQ got %d messages, want 1", len(caps))
	}
	if got := string(findTestHeader(caps[0], "x-dlq-reason")); got != "max_retries" {
		t.Errorf("x-dlq-reason = %q, want %q", got, "max_retries")
	}
}

// --- B8: retry topic forwarding with x-retry-count increment ---

func TestDispatcher_retryTopic_forwardsAndIncrementsCount(t *testing.T) {
	payload, _ := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:    "e1",
			envelope.HeaderEventType:  "events.users.v1.UserRegistered",
			envelope.HeaderRetryCount: "1",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}
	retry := &fakeWriter{}

	disp := newDisp(reader, dlq,
		events.WithRetry(retry),
		events.WithMaxRetries(3))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		return errors.New("transient")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	if len(dlq.captured()) != 0 {
		t.Errorf("DLQ got %d messages, want 0 (should retry, not DLQ)", len(dlq.captured()))
	}
	caps := retry.captured()
	if len(caps) != 1 {
		t.Fatalf("retry topic got %d messages, want 1", len(caps))
	}
	if got := string(findTestHeader(caps[0], envelope.HeaderRetryCount)); got != "2" {
		t.Errorf("x-retry-count = %q, want %q", got, "2")
	}
}

// --- B9: dedup skip on duplicate event_id ---

func TestDispatcher_dedup_skipsDuplicate(t *testing.T) {
	payload, _ := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	msg := kafka.Message{
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e1",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
	reader := &fakeReader{msgs: []kafka.Message{msg, msg}}
	dlq := &fakeWriter{}

	var calls int
	disp := newDisp(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		calls++
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = disp.Run(ctx)

	if calls != 1 {
		t.Errorf("handler invoked %d times, want 1 (dedup)", calls)
	}
}

// newDisp is events.NewDispatcher with an in-memory deduplicator and a retry
// window short enough for a unit test: a failing handler is retried for ~50 ms
// instead of the production two minutes.
func newDisp(reader events.Reader, dlq events.Writer, opts ...events.DispatcherOption) *events.Dispatcher {
	return newDispWith(events.NewMemoryDeduplicator(), reader, dlq, opts...)
}

func newDispWith(dedup events.Deduplicator, reader events.Reader, dlq events.Writer, opts ...events.DispatcherOption) *events.Dispatcher {
	fast := []events.DispatcherOption{events.WithGroup("test-group"), events.WithRetryWindow(50 * time.Millisecond), events.WithRetryBackoff(5*time.Millisecond, 10*time.Millisecond)}
	return events.NewDispatcher(reader, dlq, dedup, append(fast, opts...)...)
}
