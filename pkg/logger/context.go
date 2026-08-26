package logger

import (
	"context"

	"go.uber.org/zap"
)

// ctxKey is the unexported context key for the request-scoped logger. An
// unexported type makes collision with any other package's context value
// impossible.
type ctxKey struct{}

// IntoContext returns a copy of ctx carrying the given logger. It is the
// producer half of the request-scoped logging spine: middleware.RequestLogger
// builds a child logger holding request_id (and trace_id/span_id when a span
// is active) and stores it here, on the *request's* context.
//
// A nil logger is ignored — storing one would make FromContext return nil and
// panic every downstream caller.
func IntoContext(ctx context.Context, l *zap.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger stored on ctx.
//
// The signature takes a context.Context rather than an echo.Context on
// purpose: the layers that most need the request id — repositories, gRPC
// clients, and the Kafka consumer path, which has no echo context at all —
// only ever hold a context.Context, and pkg/logger must not drag Echo in.
//
// It never returns nil. When ctx carries no logger it falls back to the last
// logger built by New (so the line keeps its service identity), and to a no-op
// logger when New was never called — a library helper must not panic in a
// process that never configured logging.
func FromContext(ctx context.Context) *zap.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok && l != nil {
			return l
		}
	}
	if l := fallbackLogger.Load(); l != nil {
		return l
	}
	return zap.NewNop()
}
