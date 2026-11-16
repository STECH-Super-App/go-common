package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// tracerName identifies the instrumentation library in every span this
// package produces.
const tracerName = "github.com/STECH-Super-App/go-common/pkg/middleware"

// TracingOption configures Tracing.
type TracingOption func(*tracingConfig)

type tracingConfig struct{ untrustedParent bool }

// WithUntrustedParent makes every request a NEW trace root: the inbound
// traceparent is recorded as a span link but is neither the parent nor a
// sampling decision. For api-gateway only — it is the one place whose callers
// are the public. Services behind the gateway keep the default, because the
// gateway's traceparent is exactly the link request → service → outbox → relay
// → consumer they must continue.
func WithUntrustedParent() TracingOption {
	return func(c *tracingConfig) { c.untrustedParent = true }
}

// Tracing returns the fleet-wide HTTP server tracing middleware (§3c).
//
// Per request it extracts the inbound W3C traceparent, starts a server span
// named "METHOD route-template", puts the span context on c.Request().Context()
// so downstream layers (and RequestLogger's trace_id/span_id fields) see it,
// and records http.status_code on the way out.
//
// Span names follow the same cardinality law as metric labels: route
// templates, "unmatched" clamp, never raw URLs — a span name with a uuid in it
// is the §3.6 violation wearing tracing clothes (trap §9.18).
//
// When tracing.Init found no OTLP endpoint the global provider is a no-op, so
// this middleware costs almost nothing and exports nothing. The tracer is
// resolved per request rather than captured at build time on purpose: services
// commonly build their middleware chain before calling tracing.Init, and a
// tracer captured too early would stay a no-op for the process's whole life.
//
// Wire it BEFORE RequestLogger and Metrics so both see the span.
//
// # What it trusts (SL-24)
//
// Only the W3C trace context (traceparent, tracestate) is read — never
// `baggage`, whatever propagator the process installed. A client-sent baggage
// header would otherwise land on the request context and be copied into every
// outbox row, Kafka message and gRPC call that request causes.
//
// Inside the platform the inbound traceparent is a trusted remote parent: it is
// how the gateway's span becomes the parent of the service's. At the EDGE it is
// not — a caller who sets the sampled flag would force sampling whatever the
// configured ratio. api-gateway passes WithUntrustedParent.
func Tracing(opts ...TracingOption) echo.MiddlewareFunc {
	var cfg tracingConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if isOperationalRoute(c) {
				return next(c)
			}

			req := c.Request()
			route := routeTemplate(c)

			// Extract with the W3C trace-context propagator alone, never the
			// global one: baggage must not enter the context (SL-24).
			ctx := propagation.TraceContext{}.Extract(req.Context(), propagation.HeaderCarrier(req.Header))
			startOpts := []trace.SpanStartOption{
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", req.Method),
					attribute.String("http.route", route),
				),
			}
			if cfg.untrustedParent {
				// A new root, so the sampler's own ratio decides; the caller's
				// span survives as a link for anyone correlating by hand.
				if remote := trace.SpanContextFromContext(ctx); remote.IsValid() {
					startOpts = append(startOpts, trace.WithLinks(trace.Link{SpanContext: remote}))
				}
				ctx = req.Context()
				startOpts = append(startOpts, trace.WithNewRoot())
			}
			ctx, span := otel.Tracer(tracerName).Start(ctx, req.Method+" "+route, startOpts...)
			defer span.End()

			c.SetRequest(req.WithContext(ctx))

			err := next(c)

			status := responseStatus(c, err)
			span.SetAttributes(attribute.Int("http.status_code", status))
			if err != nil {
				span.RecordError(err)
			}
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(status))
			}

			return err
		}
	}
}
