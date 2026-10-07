package events_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// These tests pin ErrLeaveUncommitted: a handler that must stop without its
// message being consumed — it paused for a Postgres outage and the process is
// shutting down — gets no offset commit, no retry or DLQ copy, no failure
// counted and no dedup claim, and Run stops at once. Stopping is the point:
// kafka-go commits by position, so committing any later message of the same
// partition would consume the one left behind.

var errPostgresDown = errors.New("postgres: connection refused")

// leaveAtShutdown is what a handler paused on an outage returns once its own
// shutdown signal fires.
func leaveAtShutdown() error {
	return fmt.Errorf("paused on %w at shutdown: %w", errPostgresDown, events.ErrLeaveUncommitted)
}

// awaitRun waits for Run's result.
func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

// runToReturn runs disp on a context that stays live until the test ends, so
// a return proves Run stopped by itself rather than on a cancel.
func runToReturn(t *testing.T, disp *events.Dispatcher) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	return awaitRun(t, done)
}

// The typed handler gets its decoded message and leaves it: Run returns on a
// live context with the handler's error, commits nothing, forwards nothing and
// never fetches the second message — kafka-go would hand it out, and
// committing it would consume the first.
func TestDispatcherRun_LeaveUncommittedStopsAtOnce(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{
		userRegisteredMsg(t, "e1"), userRegisteredMsg(t, "e2"),
	}}
	dlq, retry := &fakeWriter{}, &fakeWriter{}
	disp := events.NewDispatcher(reader, dlq, events.WithRetry(retry))

	var (
		mu   sync.Mutex
		seen []string
	)
	events.Handle(disp, func(_ context.Context, e *usersv1.UserRegistered) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, e.GetEventId()+"/"+e.GetName())
		return leaveAtShutdown()
	})

	err := runToReturn(t, disp)

	require.ErrorIs(t, err, events.ErrLeaveUncommitted)
	assert.ErrorIs(t, err, errPostgresDown, "Run's error must carry the handler's cause")
	assert.NotErrorIs(t, err, context.Canceled)

	mu.Lock()
	assert.Equal(t, []string{"e1/Alice"}, seen, "only the first message reaches the handler")
	mu.Unlock()

	fetches, commitErrs := reader.snapshot()
	assert.Equal(t, 1, fetches, "nothing may be fetched after a message was left uncommitted")
	assert.Empty(t, commitErrs, "the message left uncommitted must not be committed")
	assert.Empty(t, retry.captured(), "a message left uncommitted must not reach the retry tier")
	assert.Empty(t, dlq.captured(), "a message left uncommitted must not be dead-lettered")
}

// A second Run on the same dispatcher must not resume: its reader has already
// moved past the message left behind, so the next fetch would hand out a
// later offset whose commit consumes it. Only a new reader gets it back.
func TestDispatcherRun_StaysStoppedAfterLeavingAMessage(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{
		userRegisteredMsg(t, "e1"), userRegisteredMsg(t, "e2"),
	}}
	disp := events.NewDispatcher(reader, &fakeWriter{})
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		return leaveAtShutdown()
	})

	require.ErrorIs(t, runToReturn(t, disp), events.ErrLeaveUncommitted)
	require.ErrorIs(t, runToReturn(t, disp), events.ErrLeaveUncommitted,
		"a re-run must report the stop again")

	fetches, commitErrs := reader.snapshot()
	assert.Equal(t, 1, fetches, "a re-run must not fetch the next message")
	assert.Empty(t, commitErrs)
}

// Leaving a message is not a failure-path outcome, and adding it changes none
// of those: a transient error still goes to the retry tier, or is
// dead-lettered without one, a poison pill is still dead-lettered, and each
// is committed while Run carries on. Only a returned error can leave a
// message: a panic is a handler_panic even when its value is or wraps
// ErrLeaveUncommitted.
func TestDispatcher_LeaveUncommittedAgainstTheFailurePath(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
		panicWith  any // when set, the handler panics with it instead of returning
		withRetry  bool
		wantLeave  bool   // Run stops by itself; nothing is committed
		wantRetry  int    // copies on the retry tier
		wantDLQ    int    // copies on the DLQ
		wantReason string // x-dlq-reason of the dead letter
	}{
		{
			name:       "left with a retry tier: not forwarded, not committed",
			handlerErr: leaveAtShutdown(),
			withRetry:  true,
			wantLeave:  true,
		},
		{
			name:       "left without a retry tier: not dead-lettered, not committed",
			handlerErr: leaveAtShutdown(),
			wantLeave:  true,
		},
		{
			name:       "leaving outranks a poison pill joined to it",
			handlerErr: errors.Join(events.ErrPoisonPill, leaveAtShutdown()),
			wantLeave:  true,
		},
		{
			name:       "a transient error still goes to the retry tier and is committed",
			handlerErr: errors.New("transient"),
			withRetry:  true,
			wantRetry:  1,
		},
		{
			name:       "a transient error without a retry tier is still dead-lettered and committed",
			handlerErr: errors.New("transient"),
			wantDLQ:    1,
			wantReason: "max_retries",
		},
		{
			name:       "a poison pill is still dead-lettered and committed",
			handlerErr: fmt.Errorf("bad payload: %w", events.ErrPoisonPill),
			withRetry:  true,
			wantDLQ:    1,
			wantReason: "poison_pill",
		},
		{
			name:       "a panic wrapping the sentinel is a handler panic: dead-lettered and committed",
			panicWith:  fmt.Errorf("x: %w", events.ErrLeaveUncommitted),
			withRetry:  true,
			wantDLQ:    1,
			wantReason: "handler_panic",
		},
		{
			name:       "a bare panic with the sentinel is a handler panic: dead-lettered and committed",
			panicWith:  events.ErrLeaveUncommitted,
			withRetry:  true,
			wantDLQ:    1,
			wantReason: "handler_panic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
			dlq, retry := &fakeWriter{}, &fakeWriter{}
			opts := []events.DispatcherOption{events.WithMaxRetries(3)}
			if tc.withRetry {
				opts = append(opts, events.WithRetry(retry))
			}
			disp := events.NewDispatcher(reader, dlq, opts...)
			events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
				if tc.panicWith != nil {
					panic(tc.panicWith)
				}
				return tc.handlerErr
			})

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() { done <- disp.Run(ctx) }()

			wantCommits := 1
			if tc.wantLeave {
				wantCommits = 0
				require.ErrorIs(t, awaitRun(t, done), events.ErrLeaveUncommitted)
			} else {
				// Run carries on to the next fetch; only a cancel stops it.
				require.Eventually(t, func() bool { f, _ := reader.snapshot(); return f == 2 },
					2*time.Second, 5*time.Millisecond, "Run must fetch on after a routed failure")
				cancel()
				err := awaitRun(t, done)
				require.ErrorIs(t, err, context.Canceled)
				assert.NotErrorIs(t, err, events.ErrLeaveUncommitted)
			}

			_, commitErrs := reader.snapshot()
			assert.Len(t, commitErrs, wantCommits, "offset commits")
			retried := retry.captured()
			require.Len(t, retried, tc.wantRetry, "retry tier copies")
			if tc.wantRetry > 0 {
				assert.Equal(t, "1", string(findTestHeader(retried[0], envelope.HeaderRetryCount)))
			}
			dead := dlq.captured()
			require.Len(t, dead, tc.wantDLQ, "dead letters")
			if tc.wantReason != "" {
				assert.Equal(t, tc.wantReason, string(findTestHeader(dead[0], "x-dlq-reason")))
			}
		})
	}
}

// Through the deduplicator, the handler's verdict must reach Process as an
// error so the claim rolls back with it: the redelivery after the restart —
// same event_id, same dedup store — runs the handler instead of being skipped
// as already processed.
func TestDispatcher_LeaveUncommittedRecordsNoDedupClaim(t *testing.T) {
	dedup := newFakeDedup()

	first := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	disp := events.NewDispatcher(first, &fakeWriter{}, events.WithDedup(dedup))
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		return leaveAtShutdown()
	})
	require.ErrorIs(t, runToReturn(t, disp), events.ErrLeaveUncommitted)

	dedup.mu.Lock()
	claimed := dedup.processed["e1"]
	dedup.mu.Unlock()
	require.False(t, claimed, "a message left uncommitted must leave no dedup claim")
	_, commitErrs := first.snapshot()
	assert.Empty(t, commitErrs)

	// The restart: a new reader redelivers e1 to a new dispatcher on the same store.
	handled := make(chan struct{}, 1)
	second := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	redelivery := events.NewDispatcher(second, &fakeWriter{}, events.WithDedup(dedup))
	events.Handle(redelivery, func(context.Context, *usersv1.UserRegistered) error {
		handled <- struct{}{}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- redelivery.Run(ctx) }()

	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("the redelivery was skipped as a duplicate")
	}
	require.Eventually(t, func() bool { _, c := second.snapshot(); return len(c) == 1 },
		2*time.Second, 5*time.Millisecond, "the redelivery must be committed")
	cancel()
	require.ErrorIs(t, awaitRun(t, done), context.Canceled)

	dedup.mu.Lock()
	defer dedup.mu.Unlock()
	assert.True(t, dedup.processed["e1"], "the redelivery's success records the claim")
}

// consumerCountersOf sums, per family, every events_consumer_* sample that
// carries group. Each test owns its group, but the registry is process global
// and outlives a test (-count=N), so callers compare deltas.
func consumerCountersOf(t *testing.T, group string) map[string]float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	out := map[string]float64{}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "events_consumer_") {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "group" && lp.GetValue() == group {
					out[f.GetName()] += m.GetCounter().GetValue()
				}
			}
		}
	}
	return out
}

// A message left uncommitted is neither processed nor failed, so no consumer
// family moves for it. It is logged once, at Warn, with what an operator needs
// to find the message again.
func TestDispatcher_LeaveUncommittedCountsNothingAndLogsOnce(t *testing.T) {
	const group = "events-leave-uncommitted-test"
	core, logs := observer.New(zapcore.InfoLevel)

	msg := userRegisteredMsg(t, "e1")
	msg.Partition, msg.Offset = 2, 42
	reader := &greedyReader{msgs: []kafka.Message{msg}}
	disp := events.NewDispatcher(reader, &fakeWriter{},
		events.WithRetry(&fakeWriter{}),
		events.WithGroup(group),
		events.WithLogger(zap.New(core)),
	)
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		return leaveAtShutdown()
	})

	before := consumerCountersOf(t, group)
	require.ErrorIs(t, runToReturn(t, disp), events.ErrLeaveUncommitted)

	for family, v := range consumerCountersOf(t, group) {
		assert.Equalf(t, before[family], v, "%s moved for a message left uncommitted", family)
	}

	entries := logs.FilterMessage("handler left the message uncommitted; dispatcher stopping").All()
	require.Len(t, entries, 1, "the stop is logged exactly once")
	assert.Equal(t, zapcore.WarnLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, "e1", fields["event_id"])
	assert.Equal(t, "user.events", fields["topic"])
	assert.Equal(t, group, fields["group"])
	assert.EqualValues(t, 2, fields["partition"])
	assert.EqualValues(t, 42, fields["offset"])
	assert.Contains(t, fields["error"], errPostgresDown.Error())

	// Control: the helper does see this group's samples, so the unchanged
	// values above are not vacuous.
	processedBefore := consumerCountersOf(t, group)["events_consumer_processed_total"]
	ok := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e2")}}
	control := events.NewDispatcher(ok, &fakeWriter{}, events.WithGroup(group))
	events.Handle(control, func(context.Context, *usersv1.UserRegistered) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- control.Run(ctx) }()
	require.Eventually(t, func() bool { _, c := ok.snapshot(); return len(c) == 1 },
		2*time.Second, 5*time.Millisecond)
	cancel()
	require.ErrorIs(t, awaitRun(t, done), context.Canceled)
	assert.Equal(t, processedBefore+1, consumerCountersOf(t, group)["events_consumer_processed_total"])
}

// The consumer span records the message as left behind, not as an error: a
// stop is not a fault.
func TestDispatcher_LeaveUncommittedMarksTheSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	disp := events.NewDispatcher(reader, &fakeWriter{})
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		return leaveAtShutdown()
	})

	require.ErrorIs(t, runToReturn(t, disp), events.ErrLeaveUncommitted)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "consume user.events", spans[0].Name)
	assert.Equal(t, codes.Unset, spans[0].Status.Code, "a stop must not set an error status")
	names := make([]string, 0, len(spans[0].Events))
	for _, ev := range spans[0].Events {
		names = append(names, ev.Name)
	}
	assert.Contains(t, names, "message left uncommitted")
	assert.NotContains(t, names, "exception", "a stop must not be recorded as an error")
}
