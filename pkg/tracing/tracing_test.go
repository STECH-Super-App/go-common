package tracing_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/STECH-Super-App/go-common/pkg/tracing"
)

// Test (h): with OTEL_EXPORTER_OTLP_ENDPOINT unset, Init must yield a no-op
// provider and NO error (§3c, trap §9.16).
//
// This is the property the whole design rests on: observability observes the
// system, it must never be able to take it down. A service has to boot
// identically with no Tempo present — local opt-out, test hermeticity, and no
// crashloop-on-missing-backend.
func TestInit_NoEndpointYieldsNoOpProviderAndNoError(t *testing.T) {
	require.NoError(t, unsetOTLPEndpoints(t))

	shutdown, err := tracing.Init("order-service")
	require.NoError(t, err, "a missing OTLP endpoint is not an error")
	require.NotNil(t, shutdown, "shutdown must never be nil — callers defer it unconditionally")

	_, span := otel.Tracer("test").Start(context.Background(), "probe")
	assert.False(t, span.IsRecording(), "no endpoint means no recording provider")
	span.End()

	require.NoError(t, shutdown(context.Background()))
	require.NoError(t, shutdown(context.Background()), "shutdown must be safe to call twice")
}

// Even in no-op mode the W3C propagator must be installed: propagation is
// independent of exporting, and the outbox trace hop plus every inbound
// traceparent depend on the global propagator being a real one.
func TestInit_InstallsW3CPropagatorEvenWithoutEndpoint(t *testing.T) {
	require.NoError(t, unsetOTLPEndpoints(t))

	_, err := tracing.Init("order-service")
	require.NoError(t, err)

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	assert.Equal(t,
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		carrier.Get("traceparent"),
		"the global propagator must serialize W3C trace context")

	// And the round trip: an inbound traceparent extracts to a remote parent.
	extracted := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(extracted)
	assert.True(t, sc.IsValid())
	assert.True(t, sc.IsRemote())
	assert.Equal(t, traceID, sc.TraceID())
}

// A bad endpoint must not make Init fail: the OTLP gRPC exporter dials lazily,
// so an unreachable Tempo degrades to dropped spans rather than a dead
// process.
func TestInit_UnreachableEndpointStillBoots(t *testing.T) {
	require.NoError(t, unsetOTLPEndpoints(t))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	shutdown, err := tracing.Init("order-service")
	require.NoError(t, err, "an unreachable collector must never block boot")
	require.NotNil(t, shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// The shutdown may report the flush failure; what matters is that it
	// returns rather than hanging or panicking.
	_ = shutdown(ctx)
}

// unsetOTLPEndpoints clears both endpoint variables the SDK honours, with
// t.Setenv's automatic restore.
func unsetOTLPEndpoints(t *testing.T) error {
	t.Helper()
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		t.Setenv(key, "") // registers the restore
		if err := os.Unsetenv(key); err != nil {
			return err
		}
	}
	return nil
}

// The branch above is only half the claim: prove the SDK's env handling
// actually takes effect end-to-end when OTEL_TRACES_SAMPLER is set. Sampling
// is the one knob operators will reach for at launch, and a sampler passed as
// an option by Init would silently override it.
func TestInit_EnvSamplerIsApplied(t *testing.T) {
	cases := []struct {
		sampler       string
		wantRecording bool
	}{
		{sampler: "always_off", wantRecording: false},
		{sampler: "always_on", wantRecording: true},
	}

	for _, tc := range cases {
		t.Run(tc.sampler, func(t *testing.T) {
			require.NoError(t, unsetOTLPEndpoints(t))
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
			t.Setenv("OTEL_TRACES_SAMPLER", tc.sampler)

			shutdown, err := tracing.Init("order-service")
			require.NoError(t, err)

			_, span := otel.Tracer("test").Start(context.Background(), "probe")
			assert.Equal(t, tc.wantRecording, span.IsRecording(),
				"OTEL_TRACES_SAMPLER=%s must govern the sampling decision", tc.sampler)
			span.End()

			// A short budget on purpose: the collector is unreachable, so
			// shutdown's flush will not succeed. What matters is that it
			// returns instead of hanging.
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_ = shutdown(ctx)
		})
	}
}
