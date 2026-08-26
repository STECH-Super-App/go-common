package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// counterValue returns the value of the counter sample carrying exactly the
// given labels, or 0 when no such sample exists yet. The registry is process
// global, so every assertion below is a delta.
func counterValue(t *testing.T, family string, labels map[string]string) float64 {
	t.Helper()

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			if sameLabels(m, labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func sameLabels(m *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// eventuallyCounter polls until a counter reaches want. handleOne increments
// after the handler returns, so any assertion racing a handler signal needs
// this rather than a single read.
func eventuallyCounter(t *testing.T, family string, labels map[string]string, want float64, msg string) {
	t.Helper()

	require.Eventuallyf(t, func() bool {
		return counterValue(t, family, labels) == want
	}, 2*time.Second, 10*time.Millisecond, "%s (last value: %v, want %v)",
		msg, counterValue(t, family, labels), want)
}

func userRegisteredMessage(t *testing.T, topic, eventID string) kafka.Message {
	t.Helper()

	payload, err := protojson.Marshal(&usersv1.UserRegistered{
		EventId: eventID, UserId: "u1", Name: "Alice",
	})
	require.NoError(t, err)

	return kafka.Message{
		Topic: topic,
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   eventID,
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: payload,
	}
}

// runDispatcher drives the dispatcher until every supplied message has been
// handled (or the deadline expires).
func runDispatcher(t *testing.T, disp *events.Dispatcher, want int, done <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = disp.Run(ctx) }()

	for i := 0; i < want; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatalf("dispatcher processed %d of %d messages before timeout", i, want)
		}
	}
}

// Test (f): topic comes from kafka.Message.Topic PER MESSAGE — one dispatcher
// legitimately spans a base topic and its retry tier — and group comes from
// WithGroup (§3.4).
func TestDispatcher_TagsTopicPerMessageAndGroup(t *testing.T) {
	const group = "order-review-events-consumer"

	base := map[string]string{"topic": "review.events", "group": group}
	retry := map[string]string{"topic": "review.events.retry.order", "group": group}

	beforeBase := counterValue(t, "events_consumer_processed_total", base)
	beforeRetry := counterValue(t, "events_consumer_processed_total", retry)

	reader := &fakeReader{msgs: []kafka.Message{
		userRegisteredMessage(t, "review.events", "e1"),
		userRegisteredMessage(t, "review.events.retry.order", "e2"),
	}}

	done := make(chan struct{}, 2)
	disp := events.NewDispatcher(reader, &fakeWriter{}, events.WithGroup(group))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		done <- struct{}{}
		return nil
	})

	runDispatcher(t, disp, 2, done)

	// The handler signals `done` BEFORE handleOne increments the counter, so
	// these assertions must poll rather than read once.
	eventuallyCounter(t, "events_consumer_processed_total", base, beforeBase+1,
		"the base topic must be labelled from the message, not fixed at construction")
	eventuallyCounter(t, "events_consumer_processed_total", retry, beforeRetry+1,
		"the retry tier of the same dispatcher must get its own topic label")
}

func TestDispatcher_GroupDefaultsToUnknown(t *testing.T) {
	labels := map[string]string{"topic": "user.events", "group": events.GroupUnknown}
	before := counterValue(t, "events_consumer_processed_total", labels)

	reader := &fakeReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e3")}}

	done := make(chan struct{}, 1)
	disp := events.NewDispatcher(reader, &fakeWriter{}) // no WithGroup
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		done <- struct{}{}
		return nil
	})

	runDispatcher(t, disp, 1, done)

	eventuallyCounter(t, "events_consumer_processed_total", labels, before+1,
		"an unset group must label as \"unknown\", never as an empty string")
}

func TestDispatcher_DeadLetterCounterCarriesReason(t *testing.T) {
	const group = "media"
	labels := map[string]string{"topic": "user.events", "group": group, "reason": "unmarshal_error"}
	failures := map[string]string{"topic": "user.events", "group": group}

	before := counterValue(t, "events_consumer_dead_lettered_total", labels)
	beforeFailures := counterValue(t, "events_consumer_handler_failures_total", failures)

	msg := kafka.Message{
		Topic: "user.events",
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e4",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: []byte(`{not json`),
	}

	dlq := &fakeWriter{}
	reader := &fakeReader{msgs: []kafka.Message{msg}}
	disp := events.NewDispatcher(reader, dlq, events.WithGroup(group))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = disp.Run(ctx)

	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, before+1, counterValue(t, "events_consumer_dead_lettered_total", labels))
	assert.Equal(t, beforeFailures+1, counterValue(t, "events_consumer_handler_failures_total", failures))
}

// The uncommitted-offset path — a failed DLQ write — gets its own family:
// it means the consumer group is wedged, not merely poisoned.
func TestDispatcher_FailurePathErrorsCounter(t *testing.T) {
	const group = "inbox"
	labels := map[string]string{"topic": "user.events", "group": group}
	before := counterValue(t, "events_consumer_failurepath_errors_total", labels)

	msg := kafka.Message{
		Topic: "user.events",
		Headers: kafkaHeaders(map[string]string{
			envelope.HeaderEventID:   "e5",
			envelope.HeaderEventType: "events.users.v1.UserRegistered",
		}),
		Value: []byte(`{not json`),
	}

	reader := &fakeReader{msgs: []kafka.Message{msg}}
	disp := events.NewDispatcher(reader, &failingWriter{}, events.WithGroup(group))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = disp.Run(ctx)

	assert.Equal(t, before+1, counterValue(t, "events_consumer_failurepath_errors_total", labels))
}

func TestDispatcher_DedupHitsCounter(t *testing.T) {
	const group = "auth"
	hits := map[string]string{"topic": "user.events", "group": group}
	processed := map[string]string{"topic": "user.events", "group": group}

	beforeHits := counterValue(t, "events_consumer_dedup_hits_total", hits)
	beforeProcessed := counterValue(t, "events_consumer_processed_total", processed)

	reader := &fakeReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e6")}}

	disp := events.NewDispatcher(reader, &fakeWriter{},
		events.WithGroup(group),
		events.WithDedup(alreadyProcessedDedup{}),
	)
	invoked := false
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		invoked = true
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = disp.Run(ctx)

	assert.False(t, invoked, "a deduplicated redelivery must not run the handler")
	assert.Equal(t, beforeHits+1, counterValue(t, "events_consumer_dedup_hits_total", hits))
	assert.Equal(t, beforeProcessed, counterValue(t, "events_consumer_processed_total", processed),
		"a dedup skip is not a processed message")
}

// Test (i), consumer half: the traceparent injected by the producer is read
// back as a REMOTE PARENT of the handler span, so one trace spans
// request → outbox → relay → consumer (§3c).
func TestDispatcher_ContinuesProducerTraceAsRemoteParent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))

	prevProvider := otel.GetTracerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
	})

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	producerSpanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	msg := userRegisteredMessage(t, "user.events", "e7")
	msg.Headers = append(msg.Headers, kafka.Header{
		Key:   envelope.HeaderTraceparent,
		Value: []byte("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
	})

	var handlerTraceID trace.TraceID
	done := make(chan struct{}, 1)

	reader := &fakeReader{msgs: []kafka.Message{msg}}
	disp := events.NewDispatcher(reader, &fakeWriter{}, events.WithGroup("user"))
	events.Handle(disp, func(ctx context.Context, _ *usersv1.UserRegistered) error {
		handlerTraceID = trace.SpanContextFromContext(ctx).TraceID()
		done <- struct{}{}
		return nil
	})

	runDispatcher(t, disp, 1, done)

	assert.Equal(t, traceID, handlerTraceID,
		"the handler must run inside the producer's trace, not a fresh one")

	require.Eventually(t, func() bool { return len(exporter.GetSpans()) > 0 }, time.Second, 10*time.Millisecond)
	span := exporter.GetSpans()[0]

	assert.Equal(t, "consume user.events", span.Name,
		"span names carry the topic, never the event id (trap §9.18)")
	assert.Equal(t, trace.SpanKindConsumer, span.SpanKind)
	assert.Equal(t, traceID, span.Parent.TraceID())
	assert.Equal(t, producerSpanID, span.Parent.SpanID())
	assert.True(t, span.Parent.IsRemote(), "the producer's span must be a REMOTE parent")
}

// --- test doubles ---

type failingWriter struct{}

func (failingWriter) WriteMessages(_ context.Context, _ ...kafka.Message) error {
	return errors.New("broker unavailable")
}

// alreadyProcessedDedup mimics *outbox.Deduplicator losing the claim: it
// returns nil WITHOUT running fn, which is exactly why the dispatcher cannot
// tell a dedup hit from a completion by the error alone.
type alreadyProcessedDedup struct{}

func (alreadyProcessedDedup) Process(_ context.Context, _ string, _ func() error) error {
	return nil
}

// A dispatcher built WITHOUT WithLogger must not put its Nop-derived child
// logger on the handler context: logger.FromContext would find it and silently
// swallow every handler log line, shadowing the process logger that carries
// the service field.
func TestDispatcher_WithoutLogger_HandlerFallsBackToProcessLogger(t *testing.T) {
	processLogger, err := logger.New("info", "events-fallback-test")
	require.NoError(t, err)

	var got *zap.Logger
	done := make(chan struct{}, 1)

	reader := &fakeReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e8")}}
	disp := events.NewDispatcher(reader, &fakeWriter{}) // no WithLogger
	events.Handle(disp, func(ctx context.Context, _ *usersv1.UserRegistered) error {
		got = logger.FromContext(ctx)
		done <- struct{}{}
		return nil
	})

	runDispatcher(t, disp, 1, done)

	require.NotNil(t, got)
	assert.Same(t, processLogger, got,
		"with no dispatcher logger the handler must resolve to the process logger, not a Nop")
}

// With a logger supplied, the handler context DOES carry the dispatcher's
// child — event_id, topic and the trace ids included.
func TestDispatcher_WithLogger_HandlerGetsMessageScopedLogger(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)

	done := make(chan struct{}, 1)
	reader := &fakeReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e9")}}
	disp := events.NewDispatcher(reader, &fakeWriter{},
		events.WithLogger(zap.New(core)),
		events.WithGroup("auth"),
	)
	events.Handle(disp, func(ctx context.Context, _ *usersv1.UserRegistered) error {
		logger.FromContext(ctx).Info("handler work")
		done <- struct{}{}
		return nil
	})

	runDispatcher(t, disp, 1, done)

	entries := logs.FilterMessage("handler work").All()
	require.Len(t, entries, 1, "handler logs must reach the supplied logger")

	fields := entries[0].ContextMap()
	assert.Equal(t, "e9", fields["event_id"])
	assert.Equal(t, "user.events", fields["topic"])
}

// A nil logger must be ignored rather than stored — storing it would panic on
// the first log call.
func TestDispatcher_WithNilLoggerDoesNotPanic(t *testing.T) {
	done := make(chan struct{}, 1)
	reader := &fakeReader{msgs: []kafka.Message{userRegisteredMessage(t, "user.events", "e10")}}
	disp := events.NewDispatcher(reader, &fakeWriter{}, events.WithLogger(nil))
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		done <- struct{}{}
		return nil
	})

	require.NotPanics(t, func() { runDispatcher(t, disp, 1, done) })
}
