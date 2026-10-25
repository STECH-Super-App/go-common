// Package requestid is the fleet's request-id standard: one header spelling,
// one validation rule, one context key, one place that mints an id, and the
// helpers that carry it onward over REST and gRPC.
//
// # Why it is its own package
//
// The id already travelled the fleet before this package existed, but only as
// zap FIELDS on the request-scoped logger (middleware.RequestLogger writes
// request_id there). Zap fields are write-only — there is no way to read a
// value back out of a *zap.Logger — so a layer that must FORWARD the id rather
// than log it had nothing to read. order-service, chat-service and
// tenant-service each answered that with a byte-equivalent local copy of the
// three functions below, every copy carrying the same comment saying it belongs
// here. This is that package; those copies become an import swap.
//
// It sits beside pkg/middleware rather than inside it so that pkg/middleware
// can import it (RequestLogger reads the context value) without a cycle, and so
// that the read half can keep the short, obvious name FromContext.
//
// # The standard
//
// A request id is a DIAGNOSTIC, not a credential. Nothing here can fail a
// request: an absent, over-long or malformed inbound value is replaced, never
// refused. Refusing a request because its correlation header held an odd
// character would turn an observability nicety into an availability risk, so
// this package returns no error anywhere and needs no pkg/errors reason.
//
//   - Header is the one spelling, X-Request-ID, read and written fleet-wide.
//   - Valid is the one rule: 1–128 characters of [A-Za-z0-9-]. It is exactly
//     api-gateway's long-standing regexp, so adopting this package changes no
//     value the gateway already accepts or mints.
//   - Middleware accepts a valid inbound id, mints one when there is none,
//     re-stamps the request header, echoes it on the response and parks it on
//     the request context, where middleware.RequestLogger picks it up as the
//     request_id log field.
//   - Inject (REST) and ClientInterceptor (gRPC) carry the parked id onward.
//
// # Where validation happens
//
// Every INGRESS validates — Middleware on the HTTP header, FromGRPCContext on
// incoming metadata — because that is where bytes this process did not write
// arrive. IntoContext does not: it is the low-level primitive, and a silent drop
// there would hide a caller's mistake. Egress (Inject, ClientInterceptor)
// forwards whatever is parked; the receiving service validates it on arrival,
// which is the only place with standing to.
//
// # request_id is a log FIELD, never a label
//
// Loki carries exactly two labels fleet-wide, service and level. request_id and
// trace_id stay JSON fields on the line (§3b). An id promoted to a label would
// mint one stream per request and take the ingester down, which is also why
// Valid bounds the length: an unbounded correlation value is a log-volume
// hazard on a path nobody reviews.
package requestid

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// Header is the HTTP header carrying the correlation id, read and written
	// by every service in the fleet. api-gateway stamps it on every request
	// entering the backend; middleware.RequestLogger reads this exact literal.
	//
	// net/http canonicalises header names, so the Echo constant's spelling
	// (X-Request-Id) and this one address the same header on both read and
	// write. The difference is cosmetic and exists only in source.
	Header = "X-Request-ID"

	// MetadataKey is the gRPC metadata key for the same value. gRPC normalises
	// metadata keys to lowercase, so this is the wire form — the same relation
	// middleware.LocaleMetadataKey has to middleware.LocaleHeader.
	MetadataKey = "x-request-id"

	// MaxLength bounds an accepted id. 128 characters fits a UUID (36), a
	// hex-encoded 64-byte token and every id an upstream proxy is likely to
	// stamp, while keeping the value too short to be a log-volume problem.
	MaxLength = 128
)

// Valid reports whether id may be carried as-is: 1 to MaxLength characters,
// each an ASCII letter, digit or hyphen.
//
// The character set is deliberately narrow. The value is written into log
// lines, echoed into a response header and forwarded to other services, so a
// newline, a NUL or a non-ASCII rune in it is at best noise in three places and
// at worst a forged log line. A caller who needs richer correlation data has
// the trace context for it.
//
// This is byte-for-byte the rule api-gateway has applied since the header
// existed (`^[a-zA-Z0-9\-]{1,128}$`). The loop refuses every byte >= 0x80, so
// it agrees with that regexp on multi-byte input too, where the regexp's bound
// counts runes and this one counts bytes.
func Valid(id string) bool {
	if id == "" || len(id) > MaxLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// New mints a fresh request id: a random UUIDv4, which is what api-gateway has
// always stamped. Always Valid.
func New() string {
	return uuid.NewString()
}

// ctxKey is the unexported context key. An unexported type makes collision with
// any other package's context value impossible.
type ctxKey struct{}

// IntoContext returns a copy of ctx carrying the request id.
//
// An empty id is ignored: storing one would make FromContext report a
// correlation value that does not exist, which is worse than reporting none. A
// non-empty id is stored as given — validation belongs at the ingress points
// (see the package doc), not here.
func IntoContext(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request id stored on ctx, or "" when absent.
//
// Absence is the normal case on every non-request path — a Kafka consumer, a
// background sweeper, boot-time work — so callers must treat "" as "nothing to
// forward", never as an error.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// Option configures Middleware.
type Option func(*config)

type config struct {
	generate     bool
	echoResponse bool
}

// WithoutGeneration stops the middleware minting an id when the request carries
// none, or carries one that is not Valid. Such a request then has no
// correlation id at all: nothing is parked, nothing is echoed.
//
// It exists for a service that wants api-gateway to stay the fleet's single
// stamping point, which is the stance order-service and chat-service's local
// copies were written with. Prefer the default: a minted id that lands on the
// context, the request header, the response and the access line is coherent —
// nothing can disagree with it, because nothing upstream sent one — and it
// replaces today's outcome for a direct call, which is no request_id anywhere.
func WithoutGeneration() Option {
	return func(cfg *config) { cfg.generate = false }
}

// WithoutResponseEcho stops the middleware writing the id back on the response.
//
// Only reach for it where a response header is genuinely unwanted; echoing is
// what lets a caller quote the id of a request that already finished, and the
// gateway has echoed it from the start.
func WithoutResponseEcho() Option {
	return func(cfg *config) { cfg.echoResponse = false }
}

// Middleware is the fleet's request-id middleware.
//
// Per request it:
//
//  1. takes the inbound Header value when it is Valid;
//  2. otherwise mints one with New (unless WithoutGeneration) and re-stamps it
//     onto the REQUEST header, so later middleware, handlers and anything that
//     proxies the request see the same value;
//  3. echoes it on the response (unless WithoutResponseEcho);
//  4. parks it on c.Request().Context(), where middleware.RequestLogger reads
//     it as the request_id log field and Inject/ClientInterceptor read it to
//     forward it onward.
//
// Wire it BEFORE middleware.RequestLogger — the logger reads the context value
// the moment it runs, so an id parked after it never reaches the access line.
// The fleet order is Tracing -> requestid.Middleware -> RequestLogger ->
// Metrics.
//
// It is NOT echomiddleware.RequestID. That one writes only the response header
// and never touches req.Header, so a value it mints appears in no log line, no
// span and no downstream service: someone who copies it out of a response and
// greps Loki finds nothing and concludes the log pipeline is broken. Step 2 is
// the whole difference, and it is why minting here is safe.
//
// Operational routes are not skipped, unlike the observability trio. There is
// no series or log line to protect — a probe simply gets a correlation id it
// will not use — and the alternative is duplicating the trio's route list in a
// package that has no other reason to know it.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := config{generate: true, echoResponse: true}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()

			id := req.Header.Get(Header)
			if !Valid(id) {
				if !cfg.generate {
					return next(c)
				}
				id = New()
				req.Header.Set(Header, id)
			}

			if cfg.echoResponse {
				c.Response().Header().Set(Header, id)
			}
			c.SetRequest(req.WithContext(IntoContext(req.Context(), id)))

			return next(c)
		}
	}
}

// Inject copies the request id from ctx onto an outbound HTTP request's
// headers when one is parked, leaving the headers untouched otherwise.
//
// This is what keeps one inbound request a single correlated trail across a
// REST hop to another stack — sale-service's EnsureRequestId middleware reads
// the header on arrival.
func Inject(ctx context.Context, header http.Header) {
	if id := FromContext(ctx); id != "" {
		header.Set(Header, id)
	}
}

// FromGRPCContext returns the request id from incoming gRPC metadata, or ""
// when it is absent or not Valid. If several values were sent (gRPC permits
// it), the first wins — the same rule middleware.LocaleFromGRPCContext applies.
func FromGRPCContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get(MetadataKey)
	if len(values) == 0 || !Valid(values[0]) {
		return ""
	}
	return values[0]
}

// ClientInterceptor returns a UnaryClientInterceptor that copies the request id
// parked on the calling context into outgoing gRPC metadata under MetadataKey.
// With no id on the context, no metadata is added.
//
// Attach it with grpc.WithChainUnaryInterceptor, beside
// middleware.LocaleClientInterceptor, which propagates the locale the same way.
func ClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if id := FromContext(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, MetadataKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor returns a UnaryServerInterceptor that moves the
// request id from incoming metadata onto the handler's context, so a gRPC
// server participates in the same spine its REST side does: everything below
// reads it with FromContext and forwards it with Inject or ClientInterceptor.
//
// Attach it with grpc.ChainUnaryInterceptor, beside
// middleware.MetricsUnaryServerInterceptor.
//
// It does not touch logging. A gRPC server has no equivalent of RequestLogger
// in this library, so the id lands on the context for readers and forwards, and
// a handler that wants it on its lines logs it through logger.FromContext after
// adding the field itself.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if id := FromGRPCContext(ctx); id != "" {
			ctx = IntoContext(ctx, id)
		}
		return handler(ctx, req)
	}
}
