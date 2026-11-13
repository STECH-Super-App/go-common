package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// gaugeValue reads an unlabelled gauge off the shared registry. The two outbox
// gauges are deliberately unlabelled so one dashboard panel covers both the Go
// fleet and sale-service's PHP relay.
func gaugeValue(t *testing.T, family string) float64 {
	t.Helper()

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() == family {
			require.Len(t, f.GetMetric(), 1, "%s must be a single unlabelled series", family)
			return f.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("gauge %s is not registered", family)
	return 0
}

func counterOf(t *testing.T, family string) float64 {
	t.Helper()

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() == family {
			require.Len(t, f.GetMetric(), 1)
			return f.GetMetric()[0].GetCounter().GetValue()
		}
	}
	return 0
}

// Test (e), part one: BOTH gauges must be published by the first successful
// sample, including on an empty table (§3.3).
//
// A series that only appears once a row exists makes the e2e assertion return
// an empty vector — a test ERROR, not a failure — and leaves the stall alert
// unevaluable precisely while a service is idle.
func TestSampler_PublishesBothGaugesOnEmptyResult(t *testing.T) {
	s := newSampler(func(context.Context) (int64, float64, error) {
		return 0, 0, nil // SQL: COUNT(*)=0, MIN(created_at) is NULL -> COALESCE 0
	}, time.Hour, zap.NewNop())

	s.collect(context.Background())

	assert.Equal(t, 0.0, gaugeValue(t, "outbox_pending_messages"))
	assert.Equal(t, 0.0, gaugeValue(t, "outbox_oldest_pending_age_seconds"))
}

// Test (e), part two: a non-empty result updates both gauges.
func TestSampler_UpdatesBothGaugesOnNonEmptyResult(t *testing.T) {
	s := newSampler(func(context.Context) (int64, float64, error) {
		return 42, 137.5, nil
	}, time.Hour, zap.NewNop())

	s.collect(context.Background())

	assert.Equal(t, 42.0, gaugeValue(t, "outbox_pending_messages"))
	assert.Equal(t, 137.5, gaugeValue(t, "outbox_oldest_pending_age_seconds"))

	// And back down when the backlog drains.
	s.query = func(context.Context) (int64, float64, error) { return 0, 0, nil }
	s.collect(context.Background())

	assert.Equal(t, 0.0, gaugeValue(t, "outbox_pending_messages"))
	assert.Equal(t, 0.0, gaugeValue(t, "outbox_oldest_pending_age_seconds"))
}

// A failed query must leave the last known values alone rather than reporting
// a false "0 pending": a gauge that lies about an empty backlog silences the
// stall alert exactly when the database is in trouble. Nor may it withhold
// the series: a scrape without them writes a staleness marker and resolves a
// firing OutboxStalled. The failure counter says why the values stopped.
func TestSampler_QueryErrorLeavesGaugesUntouched(t *testing.T) {
	s := newSampler(func(context.Context) (int64, float64, error) {
		return 7, 90, nil
	}, time.Hour, zap.NewNop())
	s.collect(context.Background())

	failuresBefore := counterOf(t, "outbox_pending_query_failures_total")
	s.query = func(context.Context) (int64, float64, error) {
		return 0, 0, errors.New("connection refused")
	}
	s.collect(context.Background())

	assert.Equal(t, 7.0, gaugeValue(t, "outbox_pending_messages"))
	assert.Equal(t, 90.0, gaugeValue(t, "outbox_oldest_pending_age_seconds"))
	assert.Equal(t, failuresBefore+1, counterOf(t, "outbox_pending_query_failures_total"))
}

// D23 (sale-service#208): before the first successful sample both backlog
// series are ABSENT, not 0. A plain gauge reads 0 from registration, which is
// the healthiest value OutboxStalled can see — a sampler that never got an
// answer (unmigrated table, database down from boot) looked idle and healthy.
//
// The package's own collector has usually been sampled by an earlier test,
// so this one observes a fresh collector on a private registry.
func TestBacklogGauges_AbsentUntilFirstSuccessfulSample(t *testing.T) {
	gauges := newBacklogGauges()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(gauges))

	s := newSampler(func(context.Context) (int64, float64, error) {
		return 0, 0, errors.New(`relation "outbox_messages" does not exist`)
	}, time.Hour, zap.NewNop())
	s.gauges = gauges

	failuresBefore := counterOf(t, "outbox_pending_query_failures_total")
	s.collect(context.Background())

	families, err := reg.Gather()
	require.NoError(t, err)
	assert.Empty(t, families, "a failed first sample must export nothing — never a reassuring 0")
	assert.Equal(t, failuresBefore+1, counterOf(t, "outbox_pending_query_failures_total"),
		"the failure counter is what says why the series are absent")

	s.query = func(context.Context) (int64, float64, error) { return 0, 0, nil }
	s.collect(context.Background())

	families, err = reg.Gather()
	require.NoError(t, err)
	got := map[string]float64{}
	for _, f := range families {
		require.Len(t, f.GetMetric(), 1, "%s must be a single unlabelled series", f.GetName())
		got[f.GetName()] = f.GetMetric()[0].GetGauge().GetValue()
	}
	assert.Equal(t, map[string]float64{
		"outbox_pending_messages":           0,
		"outbox_oldest_pending_age_seconds": 0,
	}, got, "the first success exposes both series, an empty table included")

	// And nothing hides them again.
	s.query = func(context.Context) (int64, float64, error) { return 0, 0, errors.New("timeout") }
	s.collect(context.Background())
	families, err = reg.Gather()
	require.NoError(t, err)
	assert.Len(t, families, 2)
}

// The failure counter and the parked counter are exported from registration
// on (value 0), unlike the backlog series: `increase(...) > 0` must be
// evaluable on a service that never failed.
func TestOutboxCounters_ExportedFromRegistration(t *testing.T) {
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, family := range []string{
		"outbox_pending_query_failures_total",
		"outbox_parked_events_total",
		"outbox_last_success_timestamp_seconds",
		"outbox_relayed_events_total",
		"outbox_relay_errors_total",
		"outbox_reaped_events_total",
	} {
		assert.True(t, names[family], "%s must be on metrics.Registry", family)
	}
}

// A sample cut short by shutdown is not a database failure: counting it would
// move outbox_pending_query_failures_total on every rolling deploy.
func TestSampler_CancelledSampleIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := newSampler(func(ctx context.Context) (int64, float64, error) {
		return 0, 0, ctx.Err()
	}, time.Hour, zap.NewNop())
	s.gauges = newBacklogGauges()

	before := counterOf(t, "outbox_pending_query_failures_total")
	s.collect(ctx)
	assert.Equal(t, before, counterOf(t, "outbox_pending_query_failures_total"))
}

func TestRecordParked_CountsParkedRows(t *testing.T) {
	before := counterOf(t, "outbox_parked_events_total")
	recordParked()
	assert.Equal(t, before+1, counterOf(t, "outbox_parked_events_total"))
}

func TestSampler_RunSamplesImmediatelyAndStopsWithContext(t *testing.T) {
	sampled := make(chan struct{}, 1)
	s := newSampler(func(context.Context) (int64, float64, error) {
		select {
		case sampled <- struct{}{}:
		default:
		}
		return 3, 12, nil
	}, time.Hour, zap.NewNop()) // interval far beyond the test: only the immediate sample can fire

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.run(ctx)
		close(done)
	}()

	select {
	case <-sampled:
	case <-time.After(2 * time.Second):
		t.Fatal("the sampler must take its first sample immediately, not after one interval")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the sampler must stop when its context is cancelled")
	}

	assert.Equal(t, 3.0, gaugeValue(t, "outbox_pending_messages"))
}

// A hand-built Config (services that do not call DefaultConfig) leaves
// MetricsInterval at zero. time.NewTicker panics on a non-positive duration,
// so the sampler must substitute the default rather than take the process down.
func TestSampler_NonPositiveIntervalFallsBackToDefault(t *testing.T) {
	s := newSampler(func(context.Context) (int64, float64, error) { return 0, 0, nil }, 0, zap.NewNop())
	assert.Equal(t, DefaultMetricsInterval, s.interval)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NotPanics(t, func() { s.run(ctx) })
}

func TestDefaultConfig_MetricsInterval(t *testing.T) {
	assert.Equal(t, DefaultMetricsInterval, DefaultConfig().MetricsInterval)
}

func TestDefaultConfig_MetricsIntervalFromEnv(t *testing.T) {
	t.Setenv("OUTBOX_METRICS_INTERVAL", "45s")
	assert.Equal(t, 45*time.Second, DefaultConfig().MetricsInterval)
}

// The relay's freshness gauge pairs with `up` to survive gauge-freeze when the
// relay goroutine dies. It must be set on every ERROR-FREE poll, including
// cycles that found zero rows — otherwise the "stale relay" alert fires
// permanently on every freshly deployed or simply idle service.
func TestRecordRelaySuccess_SetsFreshnessOnZeroRowCycles(t *testing.T) {
	before := gaugeValue(t, "outbox_last_success_timestamp_seconds")

	recordRelaySuccess(0)

	after := gaugeValue(t, "outbox_last_success_timestamp_seconds")
	assert.Greater(t, after, before, "a zero-row poll is still a successful poll")
	assert.InDelta(t, float64(time.Now().Unix()), after, 5)
}

func TestRecordRelaySuccess_CountsRelayedEvents(t *testing.T) {
	before := counterOf(t, "outbox_relayed_events_total")
	recordRelaySuccess(3)
	assert.Equal(t, before+3, counterOf(t, "outbox_relayed_events_total"))
}

func TestRecordRelayError_CountsErrors(t *testing.T) {
	before := counterOf(t, "outbox_relay_errors_total")
	recordRelayError()
	assert.Equal(t, before+1, counterOf(t, "outbox_relay_errors_total"))
}

func TestRecordReaped_CountsDeletedRows(t *testing.T) {
	before := counterOf(t, "outbox_reaped_events_total")
	recordReaped(5)
	assert.Equal(t, before+5, counterOf(t, "outbox_reaped_events_total"))
}
