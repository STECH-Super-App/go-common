package outbox

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// DefaultMetricsInterval is how often the sampler runs its aggregate query.
// Deliberately far slower than OUTBOX_POLL_INTERVAL (1s): a per-second
// aggregate query per service buys nothing that a 15s sample does not.
const DefaultMetricsInterval = 15 * time.Second

// The outbox families (§3.3). All UNLABELLED on purpose — the gauge names
// deliberately match the ones sale-service's Go relay already publishes, so a
// single alert and a single panel cover both fleets. Adding a label here would
// split that panel in two.
var (
	pendingMessages = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_pending_messages",
		Help: "Number of outbox_messages rows in status='pending'.",
	})

	// oldestPendingAge is THE stall signal: unlike a pending count it is immune
	// to load bursts, because a healthy relay keeps the oldest row young no
	// matter how many arrive.
	oldestPendingAge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_oldest_pending_age_seconds",
		Help: "Age in seconds of the oldest pending outbox row; 0 when none exist.",
	})

	// lastSuccessTimestamp pairs with `up` to survive gauge-freeze when the
	// relay goroutine dies: a dead relay stops updating gauges, so freshness —
	// not the value itself — is what says the pipeline is alive (trap §9.2).
	lastSuccessTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_last_success_timestamp_seconds",
		Help: "Unix timestamp of the last error-free relay poll, including polls that found zero rows.",
	})

	relayedEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_relayed_events_total",
		Help: "Outbox rows successfully forwarded to Kafka and marked sent.",
	})

	relayErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_relay_errors_total",
		Help: "Relay poll cycles that ended in an error.",
	})

	reapedEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_reaped_events_total",
		Help: "Sent outbox rows deleted by the reaper after the retention window.",
	})
)

func init() {
	metrics.Registry.MustRegister(
		pendingMessages,
		oldestPendingAge,
		lastSuccessTimestamp,
		relayedEvents,
		relayErrors,
		reapedEvents,
	)
}

// recordRelaySuccess marks one error-free relay poll.
//
// "Success" means err == nil, NOT processed > 0. An unset Prometheus gauge
// reads 0, so a freshness gauge that only moved on an actual publish would
// make the "relay stalled" alert fire permanently on every freshly deployed or
// simply idle service — a standing false alarm from day one (§3.3).
func recordRelaySuccess(processed int) {
	if processed > 0 {
		relayedEvents.Add(float64(processed))
	}
	lastSuccessTimestamp.SetToCurrentTime()
}

func recordRelayError() {
	relayErrors.Inc()
}

func recordReaped(deleted int64) {
	if deleted > 0 {
		reapedEvents.Add(float64(deleted))
	}
}

// statsQueryFn returns the pending-row count and the age in seconds of the
// oldest pending row (0 when there are none).
//
// It exists as a function type so the sampler can be unit-tested without
// Postgres: every test in this package is hermetic (TestStore is in-memory),
// so a sampler reaching into *Store directly would ship with no coverage at
// all. Production wiring passes Store.PendingStats.
type statsQueryFn func(ctx context.Context) (pendingCount int64, oldestPendingAgeSeconds float64, err error)

// sampler periodically publishes the two outbox gauges.
type sampler struct {
	query    statsQueryFn
	interval time.Duration
	logger   *zap.Logger
}

func newSampler(query statsQueryFn, interval time.Duration, logger *zap.Logger) *sampler {
	// A hand-built Config leaves MetricsInterval at zero, and time.NewTicker
	// panics on a non-positive duration. Observability must never be the
	// reason a service dies.
	if interval <= 0 {
		interval = DefaultMetricsInterval
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &sampler{query: query, interval: interval, logger: logger}
}

// run samples immediately, then on every tick until ctx is cancelled.
//
// The immediate first sample is what makes the gauges present from process
// start rather than up to one interval later.
func (s *sampler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.collect(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collect(ctx)
		}
	}
}

// collect runs one aggregate query and updates both gauges.
//
// On a query error the gauges are left at their last known values: reporting
// a false "0 pending" would silence the stall alert exactly when the database
// is in trouble. The paired freshness signal is `up` plus the relay's own
// last-success timestamp.
func (s *sampler) collect(ctx context.Context) {
	pending, oldestAge, err := s.query(ctx)
	if err != nil {
		s.logger.Warn("outbox metrics: pending stats query failed", zap.Error(err))
		return
	}

	pendingMessages.Set(float64(pending))
	oldestPendingAge.Set(oldestAge)
}
