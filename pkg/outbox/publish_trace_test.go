package outbox_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

// withW3CPropagator installs the propagator tracing.Init would have installed,
// and restores whatever was there before.
func withW3CPropagator(t *testing.T) {
	t.Helper()

	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
}

func sampledContext(t *testing.T) (context.Context, trace.TraceID, trace.SpanID) {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
	return ctx, traceID, spanID
}

// Test (i), producer half: PublishProto injects traceparent from the
// PRODUCING request's context into the outbox row's header map (§3c).
//
// Capturing here rather than at relay time is the whole point: the relay
// stamping its own context would root every event's trace at the relay
// process — technically a trace, semantically garbage (trap §9.17).
func TestPublishProto_InjectsTraceparentFromContext(t *testing.T) {
	withW3CPropagator(t)
	ctx, _, _ := sampledContext(t)

	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events")

	require.NoError(t, pub.PublishProto(ctx, nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1", Name: "Alice"},
	}))

	require.Len(t, store.Messages, 1)
	headers := store.Messages[0].Headers

	assert.Equal(t,
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		headers[envelope.HeaderTraceparent],
		"the row must carry the producing request's trace context")

	// The envelope keys must survive the injection unharmed.
	assert.Equal(t, "events.users.v1.UserRegistered", headers[envelope.HeaderEventType])
	assert.Equal(t, envelope.ContentTypeProtoJSON, headers[envelope.HeaderContentType])
}

// No active span means no traceparent — the header must not appear empty,
// which would make the consumer extract an invalid parent.
func TestPublishProto_NoSpanNoTraceparent(t *testing.T) {
	withW3CPropagator(t)

	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events")

	require.NoError(t, pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1", Name: "Alice"},
	}))

	require.Len(t, store.Messages, 1)
	assert.NotContains(t, store.Messages[0].Headers, envelope.HeaderTraceparent)
}

// The context's trace context wins over a caller-supplied header: causality is
// not something a call site gets to override by hand.
func TestPublishProto_ContextTraceOverridesCallerHeader(t *testing.T) {
	withW3CPropagator(t)
	ctx, traceID, _ := sampledContext(t)

	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events")

	require.NoError(t, pub.PublishProto(ctx, nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1", Name: "Alice"},
		Headers: map[string]string{
			envelope.HeaderTraceparent: "00-11111111111111111111111111111111-2222222222222222-01",
		},
	}))

	require.Len(t, store.Messages, 1)
	assert.Contains(t, store.Messages[0].Headers[envelope.HeaderTraceparent], traceID.String())
}

// The relay is transparent to the header — it copies every map entry into a
// kafka.Header verbatim — so the value the consumer reads is the value
// PublishProto wrote. envelope.Headers exposes it through a typed accessor.
func TestHeadersTraceparentAccessor(t *testing.T) {
	h := envelope.Headers{envelope.HeaderTraceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", h.Traceparent())
	assert.Empty(t, envelope.Headers{}.Traceparent())
}

// SL-24: a client-sent baggage header rides the request context in a service
// that installed a baggage-capable propagator. It must not be written into the
// outbox row (and from there into every Kafka message); traceparent still is.
func TestPublishProto_NeverCopiesBaggageIntoTheRow(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	ctx, traceID, _ := sampledContext(t)
	member, err := baggage.NewMember("tenant", "evil")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events")
	require.NoError(t, pub.PublishProto(ctx, nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1", Name: "Alice"},
		// A call site that forwards inbound headers wholesale must not smuggle it in either.
		Headers: map[string]string{"Baggage": "tenant=evil"},
	}))

	require.Len(t, store.Messages, 1)
	headers := store.Messages[0].Headers
	for key := range headers {
		assert.NotEqual(t, "baggage", strings.ToLower(key), "baggage must not be stored in an outbox row")
	}
	assert.Contains(t, headers[envelope.HeaderTraceparent], traceID.String(), "the trace link stays")
}
