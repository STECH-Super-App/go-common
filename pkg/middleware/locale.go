// Locale propagation helpers.
//
// X-User-Locale is the request-time UI-language signal. The client sends it on
// every HTTP request; the gateway forwards it as-is. Downstream services read
// it via LocaleFromContext (HTTP) or LocaleFromGRPCContext (gRPC).

package middleware

import (
	"context"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// LocaleHeader is the HTTP header carrying the requester's UI language.
	LocaleHeader = "X-User-Locale"

	// LocaleMetadataKey is the gRPC metadata key. gRPC normalizes header names
	// to lowercase, so this is the wire form.
	LocaleMetadataKey = "x-user-locale"
)

// LocaleFromContext returns the value of X-User-Locale on the incoming Echo
// request, or "" if absent. Empty string is a valid sentinel meaning "no
// preference"; downstream resolution chains apply the default.
func LocaleFromContext(c echo.Context) string {
	return c.Request().Header.Get(LocaleHeader)
}

// localeKey is unexported and typed to prevent collisions with other ctx values.
type localeKey struct{}

// WithLocale returns a derived context carrying the locale value. Used by
// LocaleMiddleware (writer) and LocaleClientInterceptor (reader).
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// localeFromCtx is the read counterpart to WithLocale. Unexported because
// LocaleClientInterceptor and the Echo middleware are the only legitimate
// readers; handlers should use LocaleFromContext (which reads the header
// directly and is pinned to the Echo context).
func localeFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(localeKey{}).(string)
	return v
}

// LocaleMiddleware reads X-User-Locale off the incoming Echo request and
// stores it in the request's context.Context, so any gRPC call made
// downstream from the handler picks it up via LocaleClientInterceptor.
func LocaleMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			locale := c.Request().Header.Get(LocaleHeader)
			ctx := WithLocale(c.Request().Context(), locale)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

// LocaleFromGRPCContext returns the locale from the x-user-locale metadata
// key on the incoming gRPC call, or "" if absent. If multiple values were
// sent (gRPC permits this), the first wins.
func LocaleFromGRPCContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	v := md.Get(LocaleMetadataKey)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// LocaleClientInterceptor returns a UnaryClientInterceptor that copies the
// locale from the calling Go context (typically set by LocaleMiddleware on
// the inbound HTTP handler) into outgoing gRPC metadata under
// LocaleMetadataKey. If the context has no locale, no metadata is added.
func LocaleClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if locale := localeFromCtx(ctx); locale != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, LocaleMetadataKey, locale)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
