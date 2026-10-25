package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/requestid"
)

// HeaderRequestID is the correlation header the API gateway stamps on every
// inbound request (keeping a syntactically valid inbound value, generating a
// UUID otherwise) and re-stamps onto the proxied request so it survives the
// hop. It is the spine every service's log lines hang off.
//
// It is an alias for requestid.Header, which is now the single definition — the
// spelling cannot drift between the middleware that reads it and the package
// that stamps it. Both names stay; this one has importers across the fleet.
const HeaderRequestID = requestid.Header

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
// request_id is read from the request context first (where
// requestid.Middleware parks it, having validated or minted it) and from the
// header otherwise, and is attached only when one of the two yields a value.
// This middleware never mints one itself: the id it logs has to be the id the
// caller can see, and only requestid.Middleware puts a minted value on the
// response and on outbound calls. A service that wants an id on its direct-call
// lines wires that middleware ahead of this one.
//
// The header value is logged as it arrived, unvalidated — zap's JSON encoder
// escapes it, so an odd value is noise in one field and nothing more, and six
// services have logged it this way since the middleware shipped. Validation is
// requestid.Middleware's job, where it also governs what gets echoed and
// forwarded.
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
			reqLogger := childLogger(req.Context(), base, resolveRequestID(req))

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

// resolveRequestID returns the id to log for this request: the one parked on
// the context, else the raw inbound header.
//
// The context wins because requestid.Middleware may have minted or rejected a
// value, and that decision is the one the caller, the response and every
// outbound call share. The header fallback is what keeps every service that
// wires RequestLogger alone behaving exactly as it did before this package
// existed. A gRPC-side id (requestid.UnaryServerInterceptor) reaches this path
// the same way, which no header read could.
func resolveRequestID(req *http.Request) string {
	if id := requestid.FromContext(req.Context()); id != "" {
		return id
	}
	return req.Header.Get(requestid.Header)
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
