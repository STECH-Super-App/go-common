package outbox

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// DefaultMetricsInterval is how often the sampler runs its aggregate query.
// Deliberately far slower than OUTBOX_POLL_INTERVAL (1s): a per-second
// aggregate query per service buys nothing that a 15s sample does not.
const DefaultMetricsInterval = 15 * time.Second

// The outbox families (§3.3). All UNLABELLED on purpose — the backlog series
// deliberately match the ones sale-service's Go relay already publishes, so a
// single alert and a single panel cover both fleets. Adding a label here would
// split that panel in two.
var (
	// backlog exports outbox_pending_messages and
	// outbox_oldest_pending_age_seconds — ABSENT until the first successful
	// sample (see backlogGauges).
	backlog = newBacklogGauges()

	// pendingQueryFailures says why the backlog series are absent, or why they
	// stopped moving. Unlike them it is exported from registration on.
	pendingQueryFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_pending_query_failures_total",
		Help: "Failed pending-backlog sample queries; while they fail, outbox_pending_messages and outbox_oldest_pending_age_seconds keep their last successful value (or stay absent before the first one).",
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

	// parkedEvents counts poison rows the relay stopped fetching (see Relay).
	// A parked row stays pending, so outbox_oldest_pending_age_seconds keeps
	// growing too; this counter is what names the cause.
	parkedEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_parked_events_total",
		Help: "Outbox rows the relay parked after OUTBOX_PARK_AFTER_FAILURES consecutive row-attributable write failures; a parked row stays pending and is skipped until the process restarts.",
	})

	reapedEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_reaped_events_total",
		Help: "Sent outbox rows deleted by the reaper after the retention window.",
	})
)

func init() {
	metrics.Registry.MustRegister(
		backlog,
		pendingQueryFailures,
		lastSuccessTimestamp,
		relayedEvents,
		relayErrors,
		parkedEvents,
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

func recordParked() {
	parkedEvents.Inc()
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

// sampler periodically publishes the two outbox backlog gauges.
type sampler struct {
	query    statsQueryFn
	interval time.Duration
	logger   *zap.Logger
	// gauges is the package's registered backlog collector; a test swaps in
	// a fresh one to observe the before-the-first-sample state, which the
	// shared registry cannot show once any other test has sampled.
	gauges *backlogGauges
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
	return &sampler{query: query, interval: interval, logger: logger, gauges: backlog}
}

// run samples immediately, then on every tick until ctx is cancelled.
//
// The immediate first sample is what makes the gauges appear as soon as the
// database first answers, rather than up to one interval later.
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
// On a query error the gauges are left at their last known values — or stay
// absent, before the first success — and outbox_pending_query_failures_total
// moves: reporting a false "0 pending" would silence the stall alert exactly
// when the database is in trouble. The paired freshness signal is `up` plus
// the relay's own last-success timestamp.
func (s *sampler) collect(ctx context.Context) {
	pending, oldestAge, err := s.query(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down: a cancelled sample is not a failing query
		}
		pendingQueryFailures.Inc()
		s.logger.Warn("outbox metrics: pending stats query failed", zap.Error(err))
		return
	}

	s.gauges.observe(float64(pending), oldestAge)
}

// backlogGauges exports outbox_pending_messages and
// outbox_oldest_pending_age_seconds from the first successful sample on.
//
// Until that sample both series are ABSENT rather than 0. Plain gauges read 0
// from registration, and 0 is the healthiest value the stall alert
// (OutboxStalled, `outbox_oldest_pending_age_seconds > 300`) can see: a
// sampler that never got an answer — an unmigrated table, a database that is
// down from boot — looked idle and healthy (sale-service#208, #291). Absent
// claims nothing; outbox_last_success_timestamp_seconds (OutboxRelaySilent)
// and outbox_pending_query_failures_total say what is wrong.
//
// After it, a failed sample writes nothing here — no sentinel, no reset to
// 0 — so both series keep the last successful value while the failure counter
// says why they stopped moving.
//
// ⚠ Do not turn a failure into withholding the series. Prometheus writes a
// staleness marker for a series a scrape no longer carries, so one failed
// sample during a real backlog would RESOLVE OutboxStalled (`for: 2m`) and
// make it earn its `for:` again: a COUNT(*) that times out once every two
// minutes — the large-backlog case — and a real stall never pages.
//
// The same shape (same names, same semantics) is what sale-service's Go
// relay exports, so one panel and one alert keep covering both fleets.
type backlogGauges struct {
	pendingDesc *prometheus.Desc
	oldestDesc  *prometheus.Desc

	mu      sync.Mutex
	known   bool
	pending float64
	oldest  float64
}

func newBacklogGauges() *backlogGauges {
	return &backlogGauges{
		pendingDesc: prometheus.NewDesc(
			"outbox_pending_messages",
			"Number of outbox_messages rows in status='pending' at the last successful sample; absent until the first one succeeds.",
			nil, nil,
		),
		oldestDesc: prometheus.NewDesc(
			"outbox_oldest_pending_age_seconds",
			"Age in seconds of the oldest pending outbox row at the last successful sample, 0 when none existed; absent until the first one succeeds.",
			nil, nil,
		),
	}
}

// observe records a successful sample. The first call exposes both series;
// nothing hides them again.
func (g *backlogGauges) observe(pending, oldestAgeSeconds float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.known = true
	g.pending = pending
	g.oldest = oldestAgeSeconds
}

// Describe implements prometheus.Collector.
func (g *backlogGauges) Describe(ch chan<- *prometheus.Desc) {
	ch <- g.pendingDesc
	ch <- g.oldestDesc
}

// Collect implements prometheus.Collector. It emits nothing until the first
// successful sample, which the registry accepts: a collector may send fewer
// metrics than it describes.
func (g *backlogGauges) Collect(ch chan<- prometheus.Metric) {
	g.mu.Lock()
	known, pending, oldest := g.known, g.pending, g.oldest
	g.mu.Unlock()

	if !known {
		return
	}

	ch <- prometheus.MustNewConstMetric(g.pendingDesc, prometheus.GaugeValue, pending)
	ch <- prometheus.MustNewConstMetric(g.oldestDesc, prometheus.GaugeValue, oldest)
}
