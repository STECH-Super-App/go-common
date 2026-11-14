package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// failingCommitReader serves msgs and fails every commit.
type failingCommitReader struct {
	mu   sync.Mutex
	msgs []kafka.Message
	i    int
}

func (r *failingCommitReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
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

func (r *failingCommitReader) CommitMessages(context.Context, ...kafka.Message) error {
	return errors.New("broker not available")
}

func runFor(disp *events.Dispatcher, d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = disp.Run(ctx)
}

// SL-08: a message whose event_type has no registered handler — or no header
// at all — was committed with a Debug line and no metric, so a renamed proto
// message made every consumer skip every event with lag 0 and nothing moving.
func TestDispatcher_CountsUnhandledEventType(t *testing.T) {
	const group = "sl08-unhandled"
	labels := map[string]string{"topic": "user.events", "group": group}
	before := counterValue(t, "events_consumer_unhandled_total", labels)

	msg := kafka.Message{
		Topic: "user.events",
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e-unhandled",
			envelope.HeaderEventType: "events.users.v1.RenamedAway",
		}),
		Value: []byte(`{}`),
	}
	disp := newDisp(&fakeReader{msgs: []kafka.Message{msg}}, &fakeWriter{}, events.WithGroup(group))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	runFor(disp, 300*time.Millisecond)

	assert.Equal(t, before+1, counterValue(t, "events_consumer_unhandled_total", labels))
}

func TestDispatcher_WarnsWhenEventTypeHeaderIsMissing(t *testing.T) {
	const group = "sl08-noheader"
	labels := map[string]string{"topic": "user.events", "group": group}
	before := counterValue(t, "events_consumer_unhandled_total", labels)

	// A PHP producer that forgot the header.
	msg := kafka.Message{Topic: "user.events", Value: []byte(`{}`),
		Headers: kafkaHeaders(map[string]string{envelope.HeaderEventID: "e-nohdr"})}

	core, logs := observer.New(zapcore.DebugLevel)
	disp := newDisp(&fakeReader{msgs: []kafka.Message{msg}}, &fakeWriter{},
		events.WithGroup(group), events.WithLogger(zap.New(core)))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	runFor(disp, 300*time.Millisecond)

	assert.Equal(t, before+1, counterValue(t, "events_consumer_unhandled_total", labels))
	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.NotEmpty(t, warns, "a missing event_type header is a producer bug, not a skipped type: Warn")
	assert.Contains(t, warns[0].Message, "event_type")
}

func TestDispatcher_KnownTypeWithoutHandlerStaysQuiet(t *testing.T) {
	// A consumer subscribed to a topic legitimately skips the types it does not
	// handle; that is counted but must not be a Warn per message.
	msg := kafka.Message{Topic: "user.events", Value: []byte(`{}`), Headers: kafkaHeaders(map[string]string{
		envelope.HeaderEventID: "e-q", envelope.HeaderEventType: "events.users.v1.Other"})}
	core, logs := observer.New(zapcore.DebugLevel)
	disp := newDisp(&fakeReader{msgs: []kafka.Message{msg}}, &fakeWriter{},
		events.WithLogger(zap.New(core)))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	runFor(disp, 300*time.Millisecond)

	assert.Empty(t, logs.FilterLevelExact(zapcore.WarnLevel).All())
}

// SL-08: commit errors were discarded (`_ = CommitMessages`).
func TestDispatcher_CountsAndLogsCommitFailure(t *testing.T) {
	const group = "sl08-commit"
	labels := map[string]string{"topic": "user.events", "group": group}
	before := counterValue(t, "events_consumer_commit_errors_total", labels)

	core, logs := observer.New(zapcore.DebugLevel)
	reader := &failingCommitReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e-commit")}}
	disp := newDisp(reader, &fakeWriter{}, events.WithGroup(group), events.WithLogger(zap.New(core)))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	runFor(disp, 300*time.Millisecond)

	assert.Equal(t, before+1, counterValue(t, "events_consumer_commit_errors_total", labels))
	require.NotEmpty(t, logs.FilterLevelExact(zapcore.ErrorLevel).All(), "a failed commit must be logged")
}
