package envelope

import "context"

// ctxKey is the unexported type used as the context.Context key for Headers.
// Unexported so external packages cannot inject or read via direct ctx.Value;
// they must go through WithHeaders / HeadersFromContext.
type ctxKey struct{}

// WithHeaders returns a new context carrying the given Headers. Used by the
// events.Dispatcher to propagate the envelope to typed handlers — handlers
// receive a typed proto in their function signature, but envelope metadata
// (occurred_at, aggregate_id, retry-count, ...) lives only on the wire and
// would otherwise be invisible to handler code.
func WithHeaders(ctx context.Context, h Headers) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, h)
}

// HeadersFromContext returns the Headers attached to ctx by WithHeaders.
// The boolean is false (and the Headers value zero) when no envelope was
// attached — handlers running outside the dispatcher (e.g. tests calling
// the handler directly without going through events.Run) get the false
// result and should construct headers explicitly.
func HeadersFromContext(ctx context.Context) (Headers, bool) {
	if ctx == nil {
		return nil, false
	}
	h, ok := ctx.Value(ctxKey{}).(Headers)
	return h, ok
}
