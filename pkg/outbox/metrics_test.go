package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

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

// Test (e), part one: BOTH gauges must be published from process start,
// including on an empty table (§3.3).
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
// stall alert exactly when the database is in trouble.
func TestSampler_QueryErrorLeavesGaugesUntouched(t *testing.T) {
	s := newSampler(func(context.Context) (int64, float64, error) {
		return 7, 90, nil
	}, time.Hour, zap.NewNop())
	s.collect(context.Background())

	s.query = func(context.Context) (int64, float64, error) {
		return 0, 0, errors.New("connection refused")
	}
	s.collect(context.Background())

	assert.Equal(t, 7.0, gaugeValue(t, "outbox_pending_messages"))
	assert.Equal(t, 90.0, gaugeValue(t, "outbox_oldest_pending_age_seconds"))
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
