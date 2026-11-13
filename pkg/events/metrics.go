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
	// retry write ITSELF failed, so this message's offset was not committed.
	// Today the dispatcher then fetches the next message, and kafka-go commits
	// by position: the next successful commit on the partition commits PAST this
	// message, which is then lost (SL-00; release 2 stops the consumer instead).
	// That is why it is its own family rather than a label on another — any
	// increase is a lost or about-to-be-lost message.
	failurePathErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumer_failurepath_errors_total",
		Help: "Failed DLQ/retry writes. The message was not forwarded and its offset was not committed, but the next successful commit on the partition commits past it: the message is lost.",
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
		unhandledTotal,
		commitErrorsTotal,
	)
}
