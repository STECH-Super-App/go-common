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
func Tracing() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if isOperationalRoute(c) {
				return next(c)
			}

			req := c.Request()
			route := routeTemplate(c)

			ctx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))
			ctx, span := otel.Tracer(tracerName).Start(ctx,
				req.Method+" "+route,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", req.Method),
					attribute.String("http.route", route),
				),
			)
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
