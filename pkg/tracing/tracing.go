// Package tracing wires the OpenTelemetry tracing pillar for STECH services.
//
// One call in main.go configures the whole process:
//
//	shutdown, err := tracing.Init("order-service")
//	if err != nil {
//	    return err
//	}
//	defer func() { _ = shutdown(context.Background()) }()
//
// After that, middleware.Tracing() produces server spans, outbox.PublishProto
// carries the trace across Kafka, and the events Dispatcher continues it on the
// consumer side — all through the global provider and propagator set here.
package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// Environment variables honoured by Init. All are the OpenTelemetry standard
// names, so operators can use published OTel documentation unchanged.
const (
	// EnvEndpoint is the switch: unset means tracing is off for this process.
	EnvEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// EnvTracesEndpoint is the signal-specific override the SDK also honours.
	EnvTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"

	envSampler    = "OTEL_TRACES_SAMPLER"
	envSamplerArg = "OTEL_TRACES_SAMPLER_ARG"
)

// defaultSamplerRatio is the launch default: keep every trace. MVP traffic is
// small enough that full fidelity costs less than the debugging time lost to
// a missing trace; the knob exists for the day that stops being true.
const defaultSamplerRatio = 1.0

// Init configures the global tracer provider and the W3C propagator for the
// process, returning a shutdown function that flushes pending spans.
//
// With OTEL_EXPORTER_OTLP_ENDPOINT (or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT)
// unset, it installs a NO-OP provider and returns a nil error: a service must
// boot identically with no Tempo present — local opt-out, test hermeticity,
// and no crashloop-on-missing-backend (trap §9.16). The W3C propagator is
// installed either way, because propagation is independent of exporting: an
// inbound traceparent must keep flowing through a process that exports
// nothing.
//
// With an endpoint set, spans go to an OTLP/gRPC exporter batched in the
// background. The exporter dials lazily, so an unreachable collector drops
// spans instead of failing the request path or blocking startup.
//
// The returned shutdown is never nil and is safe to call more than once.
func Init(serviceName string) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if endpoint() == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(context.Background())
	if err != nil {
		return func(context.Context) error { return nil },
			fmt.Errorf("tracing: create OTLP trace exporter: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(serviceResource(serviceName)),
	}
	if sampler, ok := defaultSampler(); ok {
		opts = append(opts, sdktrace.WithSampler(sampler))
	}

	provider := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

// serviceResource builds the resource every span is tagged with. service.name
// is the same identity string used as the Prometheus job and the Loki stream
// label — Tempo's search API and Grafana's log↔trace pivot both key on it.
//
// Merging with the SDK default (host, OS, telemetry SDK attributes) can fail
// when the two carry different schema URLs. That is never a reason to fail
// startup, so the failure degrades to the service-name-only resource: a span
// with fewer attributes is worth infinitely more than a service that will not
// boot (trap §9.16).
func serviceResource(serviceName string) *sdkresource.Resource {
	named := sdkresource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName))

	merged, err := sdkresource.Merge(sdkresource.Default(), named)
	if err != nil {
		logger.Warn("tracing: falling back to a minimal resource",
			zap.String("service", serviceName),
			zap.Error(err))
		return named
	}
	return merged
}

// endpoint reports the configured OTLP endpoint, honouring the signal-specific
// override the SDK also reads. An empty or absent value means "tracing off".
func endpoint() string {
	if v := os.Getenv(EnvTracesEndpoint); v != "" {
		return v
	}
	return os.Getenv(EnvEndpoint)
}

// defaultSampler returns the sampler to install when OTEL_TRACES_SAMPLER is
// NOT set, and (nil, false) when it is — in which case the SDK's own env
// handling picks the sampler and passing one here would override it.
//
// The explicit default exists because the SDK's fallback ignores
// OTEL_TRACES_SAMPLER_ARG entirely unless OTEL_TRACES_SAMPLER is also set:
// an operator who sets only the ratio would otherwise get full sampling with
// no warning.
func defaultSampler() (sdktrace.Sampler, bool) {
	if _, ok := os.LookupEnv(envSampler); ok {
		return nil, false
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplerRatio())), true
}

// samplerRatio reads OTEL_TRACES_SAMPLER_ARG, falling back to full sampling
// for an absent, unparseable or out-of-range value. A malformed ratio must not
// silently turn tracing off.
func samplerRatio() float64 {
	raw, ok := os.LookupEnv(envSamplerArg)
	if !ok {
		return defaultSamplerRatio
	}

	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return defaultSamplerRatio
	}
	return ratio
}
