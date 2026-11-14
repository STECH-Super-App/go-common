package events_test

import (
	"context"
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
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// Owner decision 14.11.2026: a recovered handler panic is a fourth class that
// dead-letters AT ONCE (events.ClassPanic, reason handler_panic): a panic is a
// bug, the same message meets it on every attempt, and release 2 spent the whole
// retry window (two minutes by default) on it while the partition waited.

const panicEventID = "e-panic"

// panicProbe counts how often the handler body ran per event id and panics for
// panicEventID.
type panicProbe struct {
	mu    sync.Mutex
	calls map[string]int
}

func newPanicProbe() *panicProbe { return &panicProbe{calls: map[string]int{}} }

func (p *panicProbe) hit(id string) {
	p.mu.Lock()
	p.calls[id]++
	p.mu.Unlock()
	if id == panicEventID {
		panic("boom in " + id)
	}
}

func (p *panicProbe) count(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[id]
}

// panicForms registers a handler of each form that panics for panicEventID, in
// the phase the form names.
var panicForms = []struct {
	name     string
	register func(d *events.Dispatcher, p *panicProbe)
}{
	{"Handle", func(d *events.Dispatcher, p *panicProbe) {
		events.Handle(d, func(_ context.Context, m *usersv1.UserRegistered) error { p.hit(m.EventId); return nil })
	}},
	{"HandleTx", func(d *events.Dispatcher, p *panicProbe) {
		events.HandleTx(d, func(_ context.Context, _ events.Tx, m *usersv1.UserRegistered) error { p.hit(m.EventId); return nil })
	}},
	{"HandlePrepared prepare", func(d *events.Dispatcher, p *panicProbe) {
		events.HandlePrepared(d,
			func(_ context.Context, m *usersv1.UserRegistered) (struct{}, error) {
				p.hit(m.EventId)
				return struct{}{}, nil
			},
			func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })
	}},
	{"HandlePrepared apply", func(d *events.Dispatcher, p *panicProbe) {
		events.HandlePrepared(d,
			func(context.Context, *usersv1.UserRegistered) (struct{}, error) { return struct{}{}, nil },
			func(_ context.Context, _ events.Tx, m *usersv1.UserRegistered, _ struct{}) error {
				p.hit(m.EventId)
				return nil
			})
	}},
}

// A panicking handler runs exactly ONCE and the message is on the DLQ with the
// panic reason — in every handler form, with and without the legacy retry tier
// (which a panic never reaches). The retry window is long: a retry would show.
func TestDispatcher_PanicRunsOnceAndIsDeadLetteredAtOnce(t *testing.T) {
	for _, form := range panicForms {
		for _, withTier := range []bool{false, true} {
			name := form.name + map[bool]string{false: "", true: ", with the retry tier"}[withTier]
			t.Run(name, func(t *testing.T) {
				reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID)}}
				dlq, tier := &fakeWriter{}, &fakeWriter{}
				opts := []events.DispatcherOption{events.WithRetryWindow(time.Minute)}
				if withTier {
					opts = append(opts, events.WithRetry(tier))
				}
				disp := newDisp(reader, dlq, opts...)
				probe := newPanicProbe()
				form.register(disp, probe)

				require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

				assert.Equal(t, 1, probe.count(panicEventID), "a panic is not retried")
				dead := dlq.captured()
				require.Len(t, dead, 1)
				assert.Equal(t, "handler_panic", string(findTestHeader(dead[0], "x-dlq-reason")))
				assert.Contains(t, string(findTestHeader(dead[0], "x-dlq-error")), "boom in "+panicEventID)
				assert.Empty(t, tier.captured(), "never the retry tier")
				_, commits := reader.snapshot()
				assert.Len(t, commits, 1, "committed once, after the dead letter")
			})
		}
	}
}

// The next message on the partition is processed at once, not after the retry
// window: the default two minutes, which release 2 spent on the panicking one.
func TestDispatcher_PanicDoesNotHoldUpTheNextMessage(t *testing.T) {
	for _, form := range panicForms {
		t.Run(form.name, func(t *testing.T) {
			reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID), userRegisteredMsg(t, "e-next")}}
			dlq := &fakeWriter{}
			disp := newDisp(reader, dlq, events.WithRetryWindow(events.DefaultRetryWindow))
			probe := newPanicProbe()
			form.register(disp, probe)

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() { done <- disp.Run(ctx) }()

			require.Eventually(t, func() bool { return probe.count("e-next") == 1 },
				2*time.Second, 5*time.Millisecond, "the message behind the panic must be handled without a retry wait")
			require.Eventually(t, func() bool { _, c := reader.snapshot(); return len(c) == 2 },
				2*time.Second, 5*time.Millisecond)
			cancel()
			require.ErrorIs(t, awaitRun(t, done), context.Canceled)

			assert.Equal(t, 1, probe.count(panicEventID))
			require.Len(t, dlq.captured(), 1)
		})
	}
}

// A panic whose dead-letter write fails still stops the consumer: nothing is
// committed and the next message is never fetched (SL-00).
func TestDispatcher_PanicWithFailedDeadLetterWriteStopsTheConsumer(t *testing.T) {
	for _, form := range panicForms {
		t.Run(form.name, func(t *testing.T) {
			reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID), userRegisteredMsg(t, "e-next")}}
			dlq := &failableWriter{fail: true}
			disp := newDisp(reader, dlq, events.WithRetryWindow(time.Minute))
			probe := newPanicProbe()
			form.register(disp, probe)

			err := runToReturn(t, disp)

			require.ErrorIs(t, err, events.ErrDeadLetterWriteFailed)
			assert.Equal(t, 1, probe.count(panicEventID))
			assert.Equal(t, 1, dlq.attemptCount(), "one write attempt, no retry")
			fetches, commits := reader.snapshot()
			assert.Equal(t, 1, fetches, "the next message is never fetched")
			assert.Empty(t, commits, "nothing is committed")
			assert.Zero(t, probe.count("e-next"))
		})
	}
}

// The stack of the panic goes to the log, with the message coordinates.
func TestDispatcher_PanicLogsTheStack(t *testing.T) {
	for _, form := range panicForms {
		t.Run(form.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)
			reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID)}}
			disp := newDisp(reader, &fakeWriter{}, events.WithLogger(zap.New(core)))
			form.register(disp, newPanicProbe())

			require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

			entries := logs.FilterMessageSnippet("handler panicked").All()
			require.Len(t, entries, 1)
			fields := entries[0].ContextMap()
			assert.Equal(t, panicEventID, fields["event_id"])
			assert.Equal(t, entries[0].Level, zapcore.ErrorLevel)
			stack, _ := fields["stack"].(string)
			assert.Contains(t, stack, "goroutine", "a stack trace")
			assert.Contains(t, stack, "consumer_panic_test.go", "it names the panicking handler's frame")
		})
	}
}

// A panic is counted as a failure and a dead letter, never as an in-place retry.
func TestDispatcher_PanicMetrics(t *testing.T) {
	topic, group := "user.events", "panic-metrics-group"
	deadLabels := map[string]string{"topic": topic, "group": group, "reason": "handler_panic"}
	retryLabels := map[string]string{"topic": topic, "group": group}
	deadBefore := counterValue(t, "events_consumer_dead_lettered_total", deadLabels)
	retriesBefore := counterValue(t, "events_consumer_inplace_retries_total", retryLabels)

	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID)}}
	disp := newDisp(reader, &fakeWriter{}, events.WithGroup(group), events.WithRetryWindow(time.Minute))
	panicForms[0].register(disp, newPanicProbe())
	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Equal(t, deadBefore+1, counterValue(t, "events_consumer_dead_lettered_total", deadLabels))
	assert.Equal(t, retriesBefore, counterValue(t, "events_consumer_inplace_retries_total", retryLabels))
}

// Against Postgres: the panicking attempt's writes are rolled back, no
// processed_events row is left, and the transaction's connection is released
// (nothing idle in a transaction, nothing still acquired).
func TestPGDispatcher_PanicRollsBackAndReleasesTheConnection(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, panicEventID)}}
	dlq := &fakeWriter{}
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq, events.WithRetryWindow(time.Minute))
	var runs int
	events.HandleTx(disp, func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered) error {
		runs++
		_, _ = tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
		panic("boom in a transaction")
	})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Equal(t, 1, runs)
	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, "handler_panic", string(findTestHeader(dlq.captured()[0], "x-dlq-reason")))
	assert.Zero(t, count(t, pool, "effects"), "the insert was rolled back")
	assert.Zero(t, count(t, pool, "processed_events"), "and the event was not marked processed")
	assert.Zero(t, idleInTransaction(t, pool))
	assert.Zero(t, pool.Stat().AcquiredConns(), "the connection went back to the pool")
}
