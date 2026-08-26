package middleware

import (
	"context"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// HeaderRequestID is the correlation header the API gateway stamps on every
// inbound request (keeping a syntactically valid inbound value, generating a
// UUID otherwise) and re-stamps onto the proxied request so it survives the
// hop. It is the spine every service's log lines hang off.
const HeaderRequestID = "X-Request-ID"

// accessLogMessage is the msg field of the one access line per request. Kept
// stable because operators grep for it.
const accessLogMessage = "http request"

// RequestLoggerOption configures optional RequestLogger behaviour.
type RequestLoggerOption func(*requestLoggerConfig)

type requestLoggerConfig struct {
	upstream func(c echo.Context) string
}

// WithUpstream adds an "upstream" field to the access line, resolved per
// request. Only the API gateway sets it — it names the service a request was
// proxied to, which a leaf service has no notion of.
func WithUpstream(fn func(c echo.Context) string) RequestLoggerOption {
	return func(cfg *requestLoggerConfig) { cfg.upstream = fn }
}

// RequestLogger returns the fleet-wide request logging middleware (§3b).
//
// It does two things per request:
//
//  1. Builds a child logger carrying request_id (from X-Request-ID) and, when
//     a span is active on the request context, trace_id and span_id — then
//     stores it on c.Request().Context() via logger.IntoContext, so every
//     layer below (handlers, application services, repositories, gRPC clients)
//     can log correlated lines with logger.FromContext(ctx).
//  2. Emits exactly ONE INFO access line per request, carrying route (the Echo
//     template, clamped to "unmatched"), method, status, duration_ms — plus
//     upstream when WithUpstream is set.
//
// It REPLACES the older Logger middleware at call sites; running both doubles
// log volume and makes "one line per request" false on day one.
//
// Operational routes (/metrics, /health, /livez, /readyz) are skipped, same
// rule and reason as Metrics().
//
// Wire it AFTER Tracing() in the chain, so the span it reads already exists.
//
// request_id is attached only when the header is present. The gateway is the
// single stamping point by design — minting a second id per service would
// produce a correlation spine that silently disagrees with itself.
func RequestLogger(base *zap.Logger, opts ...RequestLoggerOption) echo.MiddlewareFunc {
	cfg := &requestLoggerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if isOperationalRoute(c) {
				return next(c)
			}

			req := c.Request()
			reqLogger := childLogger(req.Context(), base, req.Header.Get(HeaderRequestID))

			c.SetRequest(req.WithContext(logger.IntoContext(req.Context(), reqLogger)))

			start := time.Now()
			err := next(c)
			elapsed := time.Since(start)

			fields := []zap.Field{
				zap.String("route", routeTemplate(c)),
				zap.String("method", req.Method),
				zap.Int("status", responseStatus(c, err)),
				zap.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
			}
			if cfg.upstream != nil {
				fields = append(fields, zap.String("upstream", cfg.upstream(c)))
			}

			reqLogger.Info(accessLogMessage, fields...)

			return err
		}
	}
}

// childLogger derives the request-scoped logger. A nil base falls back to the
// process logger rather than panicking — an unconfigured logger must never be
// the reason a request fails.
func childLogger(ctx context.Context, base *zap.Logger, requestID string) *zap.Logger {
	if base == nil {
		base = logger.FromContext(ctx)
	}

	fields := make([]zap.Field, 0, 3)
	if requestID != "" {
		fields = append(fields, zap.String("request_id", requestID))
	}

	// trace_id/span_id are what join a log line to its trace in Grafana. They
	// are only meaningful when a span is actually active — an invalid span
	// context would write all-zero ids that link nowhere.
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		fields = append(fields,
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}

	if len(fields) == 0 {
		return base
	}
	return base.With(fields...)
}
