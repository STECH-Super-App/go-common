package events

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// The six consumer families (§3.4). All are registered at package init on the
// shared metrics.Registry — never inside NewDispatcher, which service tests
// construct many times per process (§3.0, trap §9.4).
//
// Labels: topic is read per message off kafka.Message.Topic (one dispatcher
// legitimately spans a base topic and its retry tier), group is the Kafka
// GroupID passed verbatim via WithGroup. Nothing else is allowed on these
// families (§3.6).
var (
	processedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_processed_total",
		Help: "Messages whose handler completed successfully and whose offset was committed.",
	}, []string{"topic", "group"})

	handlerFailuresTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_handler_failures_total",
		Help: "Messages whose handler returned an error, panicked, or failed to unmarshal.",
	}, []string{"topic", "group"})

	retriedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_retried_total",
		Help: "Messages forwarded to the retry topic with an incremented retry count.",
	}, []string{"topic", "group"})

	deadLetteredTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_dead_lettered_total",
		Help: "Messages written to the dead-letter topic, by failure reason.",
	}, []string{"topic", "group", "reason"})

	dedupHitsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_dedup_hits_total",
		Help: "Redeliveries skipped because the event_id was already processed.",
	}, []string{"topic", "group"})

	// failurePathErrorsTotal is the scariest path in the pipeline: the DLQ or
	// retry write ITSELF failed, so the offset was deliberately left
	// uncommitted. It means the consumer group is wedged, not merely poisoned,
	// which is why it is its own family rather than a label on another.
	failurePathErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_failurepath_errors_total",
		Help: "Failed DLQ/retry writes that left the source offset uncommitted for redelivery.",
	}, []string{"topic", "group"})
)

func init() {
	metrics.Registry.MustRegister(
		processedTotal,
		handlerFailuresTotal,
		retriedTotal,
		deadLetteredTotal,
		dedupHitsTotal,
		failurePathErrorsTotal,
	)
}
