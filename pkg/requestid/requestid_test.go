package requestid_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/STECH-Super-App/go-common/pkg/middleware"
	"github.com/STECH-Super-App/go-common/pkg/requestid"
)

// testID is the stand-in for a gateway-stamped id. A const because goconst
// counts a literal in == / != / case / return positions across the whole
// package, test files included.
const testID = "req-abc-123"

// gatewayRegexp is api-gateway's long-standing validation rule, copied
// verbatim from internal/middleware/request_id.go. Valid must agree with it on
// every input, or adopting this package would change which ids the fleet
// accepts.
var gatewayRegexp = regexp.MustCompile(`^[a-zA-Z0-9\-]{1,128}$`)

// validityVectors is shared by TestValid and the gateway-agreement test, so a
// case added for one is checked by both.
var validityVectors = []struct {
	name string
	id   string
	want bool
}{
	{name: "uuid", id: "0f8fad5b-d9cb-469f-a165-70867728950e", want: true},
	{name: "alphanumeric", id: "abcXYZ0189", want: true},
	{name: "single character", id: "a", want: true},
	{name: "hyphens only", id: "---", want: true},
	{name: "exactly the length cap", id: strings.Repeat("a", 128), want: true},

	{name: "empty", id: "", want: false},
	{name: "one over the length cap", id: strings.Repeat("a", 129), want: false},
	{name: "underscore", id: "req_abc", want: false},
	{name: "space", id: "req abc", want: false},
	{name: "dot", id: "req.abc", want: false},
	{name: "slash", id: "req/abc", want: false},
	{name: "newline", id: "req\nabc", want: false},
	{name: "carriage return", id: "req\rabc", want: false},
	{name: "tab", id: "req\tabc", want: false},
	{name: "NUL", id: "req\x00abc", want: false},
	{name: "non-ASCII rune", id: "reqé", want: false},
	{name: "cyrillic", id: "запрос", want: false},
	{name: "leading whitespace around a valid id", id: " " + testID, want: false},
}

func TestValid(t *testing.T) {
	for _, tc := range validityVectors {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, requestid.Valid(tc.id))
		})
	}
}

// Valid is documented as byte-for-byte api-gateway's regexp. That claim is what
// makes adopting this package a no-op for every id already in flight, so it is
// pinned rather than asserted in prose.
func TestValid_AgreesWithGatewayRegexp(t *testing.T) {
	for _, tc := range validityVectors {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, gatewayRegexp.MatchString(tc.id), requestid.Valid(tc.id),
				"Valid and the gateway regexp disagree on %q", tc.id)
		})
	}
}

func TestNew(t *testing.T) {
	first := requestid.New()

	assert.True(t, requestid.Valid(first), "a minted id must pass Valid: %q", first)
	assert.NotEqual(t, first, requestid.New(), "New must not repeat itself")
}

func TestIntoContextFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want string
	}{
		{
			name: "value round-trips",
			ctx:  func() context.Context { return requestid.IntoContext(context.Background(), testID) },
			want: testID,
		},
		{
			name: "empty id is not stored",
			ctx:  func() context.Context { return requestid.IntoContext(context.Background(), "") },
			want: "",
		},
		{
			name: "absent on a bare context",
			ctx:  context.Background,
			want: "",
		},
		{
			name: "nil context does not panic",
			ctx:  func() context.Context { return nil },
			want: "",
		},
		{
			name: "last write wins",
			ctx: func() context.Context {
				return requestid.IntoContext(requestid.IntoContext(context.Background(), "first"), testID)
			},
			want: testID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, requestid.FromContext(tc.ctx()))
		})
	}
}

// "An empty id is ignored" is invisible through FromContext — a stored "" reads
// back as "" either way — so it is pinned on the only thing that can see it:
// the context is returned untouched, with no value wrapper allocated around it.
func TestIntoContext_EmptyIDReturnsTheSameContext(t *testing.T) {
	parent := context.Background()

	assert.True(t, requestid.IntoContext(parent, "") == parent,
		"an empty id must not wrap the context")
	assert.False(t, requestid.IntoContext(parent, testID) == parent,
		"a real id must wrap the context")
}

// echoRequest drives one request through the middleware under test and reports
// everything the middleware is responsible for.
type result struct {
	ctxID      string
	reqHeader  string
	resHeader  string
	handlerRan bool
}

func run(t *testing.T, inbound string, opts ...requestid.Option) result {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/listings/42", nil)
	if inbound != "" {
		req.Header.Set(requestid.Header, inbound)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	var got result
	handler := func(c echo.Context) error {
		got.handlerRan = true
		got.ctxID = requestid.FromContext(c.Request().Context())
		got.reqHeader = c.Request().Header.Get(requestid.Header)
		return c.NoContent(http.StatusNoContent)
	}

	require.NoError(t, requestid.Middleware(opts...)(handler)(c))
	got.resHeader = rec.Header().Get(requestid.Header)

	return got
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		inbound string
		opts    []requestid.Option

		// wantID is the exact id expected everywhere; empty means "a minted
		// id", which is checked by shape instead of by value.
		wantID      string
		wantMinted  bool
		wantEchoed  bool
		wantOnCtx   bool
		wantRestamp bool
	}{
		{
			name:        "absent id is generated, parked, re-stamped and echoed",
			inbound:     "",
			wantMinted:  true,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "valid inbound id is preserved verbatim",
			inbound:     testID,
			wantID:      testID,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "uuid inbound id is preserved verbatim",
			inbound:     "0f8fad5b-d9cb-469f-a165-70867728950e",
			wantID:      "0f8fad5b-d9cb-469f-a165-70867728950e",
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "over-long id is replaced, not refused",
			inbound:     strings.Repeat("a", 129),
			wantMinted:  true,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "id with a newline is replaced",
			inbound:     "req\nabc",
			wantMinted:  true,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "id with an underscore is replaced",
			inbound:     "req_abc",
			wantMinted:  true,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:    "WithoutGeneration leaves an absent id absent",
			inbound: "",
			opts:    []requestid.Option{requestid.WithoutGeneration()},
		},
		{
			name:    "WithoutGeneration drops a malformed id rather than carrying it",
			inbound: "req_abc",
			opts:    []requestid.Option{requestid.WithoutGeneration()},
		},
		{
			name:        "WithoutGeneration still carries a valid id",
			inbound:     testID,
			opts:        []requestid.Option{requestid.WithoutGeneration()},
			wantID:      testID,
			wantEchoed:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "WithoutResponseEcho keeps the id off the response only",
			inbound:     testID,
			opts:        []requestid.Option{requestid.WithoutResponseEcho()},
			wantID:      testID,
			wantOnCtx:   true,
			wantRestamp: true,
		},
		{
			name:        "WithoutResponseEcho still generates",
			inbound:     "",
			opts:        []requestid.Option{requestid.WithoutResponseEcho()},
			wantMinted:  true,
			wantOnCtx:   true,
			wantRestamp: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.inbound, tc.opts...)

			require.True(t, got.handlerRan, "the middleware must never short-circuit a request")

			want := tc.wantID
			if tc.wantMinted {
				require.NotEmpty(t, got.ctxID, "an id should have been minted")
				require.NotEqual(t, tc.inbound, got.ctxID, "the rejected inbound value must not survive")
				require.True(t, requestid.Valid(got.ctxID), "a minted id must be Valid: %q", got.ctxID)
				want = got.ctxID
			}

			if tc.wantOnCtx {
				assert.Equal(t, want, got.ctxID, "context value")
			} else {
				assert.Empty(t, got.ctxID, "nothing should be parked on the context")
			}

			if tc.wantRestamp {
				assert.Equal(t, want, got.reqHeader, "request header seen by the handler")
			} else {
				assert.Equal(t, tc.inbound, got.reqHeader, "the request header must be left as it arrived")
			}

			if tc.wantEchoed {
				assert.Equal(t, want, got.resHeader, "response header")
			} else {
				assert.Empty(t, got.resHeader, "nothing should be echoed")
			}
		})
	}
}

// The whole point of re-stamping the request header is that a value this
// middleware minted is not a decoy: it must be the same value in the response,
// in the log line and on anything forwarded onward.
func TestMiddleware_GeneratedIDIsTheSameEverywhere(t *testing.T) {
	got := run(t, "")

	require.NotEmpty(t, got.ctxID)
	assert.Equal(t, got.ctxID, got.reqHeader, "request header")
	assert.Equal(t, got.ctxID, got.resHeader, "response header")
}

// The contract with pkg/logger: the id this middleware resolves is the one
// RequestLogger writes as the request_id FIELD of the access line (a field, not
// a Loki label — Loki carries only service and level).
func TestMiddleware_FeedsRequestLoggerField(t *testing.T) {
	tests := []struct {
		name    string
		inbound string
		// wantSuppliedID pins the exact value; otherwise the generated id is
		// read back off the response and compared.
		wantSuppliedID string
	}{
		{name: "supplied id reaches the log field", inbound: testID, wantSuppliedID: testID},
		{name: "generated id reaches the log field", inbound: ""},
		{name: "replaced malformed id reaches the log field", inbound: "req_abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)

			e := echo.New()
			e.Use(requestid.Middleware())
			e.Use(middleware.RequestLogger(zap.New(core)))
			e.GET("/listings/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })

			req := httptest.NewRequest(http.MethodGet, "/listings/42", nil)
			if tc.inbound != "" {
				req.Header.Set(requestid.Header, tc.inbound)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			want := tc.wantSuppliedID
			if want == "" {
				want = rec.Header().Get(requestid.Header)
				require.NotEmpty(t, want, "the middleware should have echoed a generated id")
				require.NotEqual(t, tc.inbound, want)
			}

			entries := logs.All()
			require.Len(t, entries, 1, "exactly one access line per request")
			assert.Equal(t, want, entries[0].ContextMap()["request_id"])
		})
	}
}

// RequestLogger alone must behave exactly as it did before this package
// existed: the raw header, logged as it arrived, and no id at all when the
// header is absent. Six services wire it that way and none of them should
// change on a version bump.
func TestRequestLoggerWithoutTheMiddleware_IsUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		inbound string
		want    any
	}{
		{name: "header is logged", inbound: testID, want: testID},
		{name: "malformed header is still logged verbatim", inbound: "req_abc", want: "req_abc"},
		{name: "absent header logs no field", inbound: "", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)

			e := echo.New()
			e.Use(middleware.RequestLogger(zap.New(core)))
			e.GET("/listings/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })

			req := httptest.NewRequest(http.MethodGet, "/listings/42", nil)
			if tc.inbound != "" {
				req.Header.Set(middleware.HeaderRequestID, tc.inbound)
			}
			e.ServeHTTP(httptest.NewRecorder(), req)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tc.want, entries[0].ContextMap()["request_id"])
		})
	}
}

// RequestLogger reads the CONTEXT first and the header only as a fallback.
// That ordering is not academic: an id parked without the header being stamped
// — what requestid.UnaryServerInterceptor does, and what any future middleware
// resolving an id from somewhere other than this header would do — is reachable
// no other way, and a header that disagrees with the parked value must lose,
// because the parked value is the one the response and every outbound call
// carry.
func TestRequestLogger_PrefersTheContextOverTheHeader(t *testing.T) {
	tests := []struct {
		name     string
		parked   string
		inbound  string
		wantLine string
	}{
		{name: "context only, no header at all", parked: testID, wantLine: testID},
		{name: "context wins over a disagreeing header", parked: testID, inbound: "from-header", wantLine: testID},
		{name: "header is used when nothing is parked", inbound: "from-header", wantLine: "from-header"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)

			park := func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					req := c.Request()
					c.SetRequest(req.WithContext(requestid.IntoContext(req.Context(), tc.parked)))
					return next(c)
				}
			}

			e := echo.New()
			e.Use(park)
			e.Use(middleware.RequestLogger(zap.New(core)))
			e.GET("/listings/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })

			req := httptest.NewRequest(http.MethodGet, "/listings/42", nil)
			if tc.inbound != "" {
				req.Header.Set(requestid.Header, tc.inbound)
			}
			e.ServeHTTP(httptest.NewRecorder(), req)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tc.wantLine, entries[0].ContextMap()["request_id"])
		})
	}
}

func TestInject(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		existing string
		want     string
	}{
		{
			name: "parked id is injected",
			ctx:  requestid.IntoContext(context.Background(), testID),
			want: testID,
		},
		{
			name: "no parked id leaves the headers untouched",
			ctx:  context.Background(),
			want: "",
		},
		{
			name:     "a parked id replaces a stale header",
			ctx:      requestid.IntoContext(context.Background(), testID),
			existing: "stale",
			want:     testID,
		},
		{
			name:     "no parked id does not clear an existing header",
			ctx:      context.Background(),
			existing: "kept",
			want:     "kept",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.existing != "" {
				header.Set(requestid.Header, tc.existing)
			}

			requestid.Inject(tc.ctx, header)

			assert.Equal(t, tc.want, header.Get(requestid.Header))
		})
	}
}

func TestFromGRPCContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "no metadata at all",
			ctx:  context.Background(),
			want: "",
		},
		{
			name: "metadata without the key",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-user-locale", "ru")),
			want: "",
		},
		{
			name: "valid id",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestid.MetadataKey, testID)),
			want: testID,
		},
		{
			name: "malformed id is refused",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestid.MetadataKey, "req_abc")),
			want: "",
		},
		{
			name: "over-long id is refused",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs(requestid.MetadataKey, strings.Repeat("a", 129))),
			want: "",
		},
		{
			name: "first of several values wins",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs(requestid.MetadataKey, testID, requestid.MetadataKey, "second")),
			want: testID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, requestid.FromGRPCContext(tc.ctx))
		})
	}
}

func TestClientInterceptor(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{
			name: "parked id becomes outgoing metadata",
			ctx:  requestid.IntoContext(context.Background(), testID),
			want: []string{testID},
		},
		{
			name: "no parked id adds no metadata",
			ctx:  context.Background(),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sent []string
			invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				md, ok := metadata.FromOutgoingContext(ctx)
				if ok {
					sent = md.Get(requestid.MetadataKey)
				}
				return nil
			}

			err := requestid.ClientInterceptor()(tc.ctx, "/svc/Method", nil, nil, nil, invoker)

			require.NoError(t, err)
			assert.Equal(t, tc.want, sent)
		})
	}
}

func TestUnaryServerInterceptor(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "incoming metadata lands on the handler context",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestid.MetadataKey, testID)),
			want: testID,
		},
		{
			name: "malformed metadata parks nothing",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestid.MetadataKey, "req_abc")),
			want: "",
		},
		{
			name: "no metadata parks nothing",
			ctx:  context.Background(),
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			handler := func(ctx context.Context, _ any) (any, error) {
				seen = requestid.FromContext(ctx)
				return nil, nil
			}

			_, err := requestid.UnaryServerInterceptor()(tc.ctx, nil, &grpc.UnaryServerInfo{}, handler)

			require.NoError(t, err)
			assert.Equal(t, tc.want, seen)
		})
	}
}

// The gRPC server interceptor and the HTTP middleware must park on the SAME
// key, or an id that arrives over gRPC is invisible to the REST-side helpers
// (and to RequestLogger's context read). Two unexported keys in two packages is
// exactly the bug this package was created to retire.
func TestGRPCAndHTTPShareOneContextKey(t *testing.T) {
	var forwarded http.Header

	handler := func(ctx context.Context, _ any) (any, error) {
		forwarded = http.Header{}
		requestid.Inject(ctx, forwarded)
		return nil, nil
	}

	incoming := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(requestid.MetadataKey, testID))

	_, err := requestid.UnaryServerInterceptor()(incoming, nil, &grpc.UnaryServerInfo{}, handler)

	require.NoError(t, err)
	assert.Equal(t, testID, forwarded.Get(requestid.Header),
		"an id received over gRPC must be forwardable over REST")
}

// The alias is load-bearing: api-gateway binds its stamping header to
// middleware.HeaderRequestID at compile time precisely so the two cannot drift.
func TestHeaderAliasMatches(t *testing.T) {
	assert.Equal(t, requestid.Header, middleware.HeaderRequestID)
	assert.Equal(t, strings.ToLower(requestid.Header), requestid.MetadataKey,
		"the gRPC metadata key is the lower-cased header name")
}
