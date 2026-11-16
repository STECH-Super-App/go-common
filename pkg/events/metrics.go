package events

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// The consumer families (§3.4). All are registered at package init on the
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
		Help: "Messages forwarded to the legacy retry topic (WithRetry) with an incremented retry count.",
	}, []string{"topic", "group"})

	deadLetteredTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_dead_lettered_total",
		Help: "Messages written to the dead-letter topic, by failure reason.",
	}, []string{"topic", "group", "reason"})

	dedupHitsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_dedup_hits_total",
		Help: "Redeliveries skipped because the event_id was already processed.",
	}, []string{"topic", "group"})

	// unhandledTotal counts messages skipped because no handler is registered
	// for their event_type header (or the header is missing). Most of it is
	// legitimate; a sudden rise on a topic that used to be handled is a renamed
	// or moved proto message (SL-08).
	unhandledTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_unhandled_total",
		Help: "Messages committed without running a handler: no handler for their event_type, or no event_type header.",
	}, []string{"topic", "group"})

	// commitErrorsTotal counts offset commits that failed after the message was
	// handled or dead-lettered.
	commitErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_commit_errors_total",
		Help: "Offset commits that returned an error.",
	}, []string{"topic", "group"})

	// failurePathErrorsTotal is the scariest path in the pipeline: the DLQ or
	// retry write ITSELF failed. Since release 2 the dispatcher then STOPS
	// without committing (Run returns ErrDeadLetterWriteFailed and the process
	// exits non-zero), so the message is redelivered after the restart and
	// nothing is lost. Before it, the dispatcher went on to the next message and
	// the next commit moved the offset past the failed one (SL-00). Any increase
	// is a consumer that just stopped: alert on it, and on the restart.
	failurePathErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_failurepath_errors_total",
		Help: "Failed DLQ/retry writes. The consumer stopped without committing the message and exits non-zero, so Kafka redelivers it after the restart.",
	}, []string{"topic", "group"})

	// inPlaceRetriesTotal counts the attempts that failed with a retryable error
	// and were scheduled for another try. A steady trickle is a flaky
	// dependency; a message that exhausts them ends as
	// events_consumer_dead_lettered_total{reason="max_retries"}.
	inPlaceRetriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_inplace_retries_total",
		Help: "Failed handler attempts that were scheduled for another in-place attempt.",
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
		inPlaceRetriesTotal,
		unhandledTotal,
		commitErrorsTotal,
	)
}
