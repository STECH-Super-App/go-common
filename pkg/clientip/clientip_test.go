package clientip_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/STECH-Super-App/go-common/pkg/clientip"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // "" = absent
	}{
		{"ipv4", "203.0.113.7", "203.0.113.7"},
		{"ipv6", "2001:db8::1", "2001:db8::1"},
		{"ipv6 uppercase is canonicalised", "2001:DB8::A", "2001:db8::a"},
		{"v4-mapped v6 is unmapped", "::ffff:203.0.113.7", "203.0.113.7"},
		{"loopback is a valid client in local dev", "127.0.0.1", "127.0.0.1"},
		{"private is a valid client behind a private NAT", "10.1.2.3", "10.1.2.3"},
		{"empty", "", ""},
		{"spaces", " 203.0.113.7", ""},
		{"list", "203.0.113.7, 198.51.100.1", ""},
		{"host:port", "203.0.113.7:443", ""},
		{"hostname", "evil.example", ""},
		{"zone", "fe80::1%eth0", ""},
		{"unspecified v4", "0.0.0.0", ""},
		{"unspecified v6", "::", ""},
		{"multicast", "224.0.0.1", ""},
		{"garbage", "999.1.1.1", ""},
		{"newline", "203.0.113.7\n", ""},
		{"over the length cap", strings.Repeat("1", clientip.MaxLen+1), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clientip.Parse(tt.raw)
			if tt.want == "" {
				assert.False(t, ok)
				assert.False(t, got.IsValid())
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestFromRequest_ReadsOnlyXClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.9:5555"
	// Every other source a naive reader would believe.
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	r.Header.Set("X-Real-IP", "192.0.2.2")
	r.Header.Set("Forwarded", "for=192.0.2.3")

	addr, ok := clientip.FromRequest(r)
	assert.False(t, ok, "no X-Client-IP: absent, never the peer or a forwarding header")
	assert.False(t, addr.IsValid())

	r.Header.Set(clientip.Header, "203.0.113.7")
	addr, ok = clientip.FromRequest(r)
	require.True(t, ok)
	assert.Equal(t, "203.0.113.7", addr.String())
}

func TestFromRequest_RefusesAmbiguousOrInvalid(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add(clientip.Header, "203.0.113.7")
	r.Header.Add(clientip.Header, "198.51.100.1")
	_, ok := clientip.FromRequest(r)
	assert.False(t, ok, "two values: which one is the gateway's is unknowable")

	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(clientip.Header, "not-an-ip")
	_, ok = clientip.FromRequest(r)
	assert.False(t, ok)

	_, ok = clientip.FromRequest(nil)
	assert.False(t, ok)
}

func TestFromEcho(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(clientip.Header, "2001:db8::7")
	c := e.NewContext(req, httptest.NewRecorder())

	addr, ok := clientip.FromEcho(c)
	require.True(t, ok)
	assert.Equal(t, "2001:db8::7", addr.String())
}

func TestSet_ReplacesWhateverTheClientSent(t *testing.T) {
	h := http.Header{}
	h.Add(clientip.Header, "1.1.1.1")
	h.Add(clientip.Header, "2.2.2.2")

	clientip.Set(h, netip.MustParseAddr("203.0.113.7"))
	assert.Equal(t, []string{"203.0.113.7"}, h.Values(clientip.Header))

	// An address the gateway could not establish must leave NO header, not the
	// client's own value.
	clientip.Set(h, netip.Addr{})
	assert.Empty(t, h.Values(clientip.Header))

	h.Set(clientip.Header, "9.9.9.9")
	clientip.Set(h, netip.MustParseAddr("::"))
	assert.Empty(t, h.Values(clientip.Header), "an unusable address is not forwarded")
}

func TestContextRoundTrip(t *testing.T) {
	_, ok := clientip.FromContext(context.Background())
	assert.False(t, ok)

	ctx := clientip.NewContext(context.Background(), netip.MustParseAddr("203.0.113.7"))
	addr, ok := clientip.FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "203.0.113.7", addr.String())

	// An invalid address is not stored.
	ctx = clientip.NewContext(context.Background(), netip.Addr{})
	_, ok = clientip.FromContext(ctx)
	assert.False(t, ok)
}

func TestMiddleware_PutsAddressOnRequestContext(t *testing.T) {
	e := echo.New()
	var got netip.Addr
	var ok bool
	e.Use(clientip.Middleware())
	e.GET("/", func(c echo.Context) error {
		got, ok = clientip.FromContext(c.Request().Context())
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(clientip.Header, "203.0.113.7")
	e.ServeHTTP(httptest.NewRecorder(), req)
	require.True(t, ok)
	assert.Equal(t, "203.0.113.7", got.String())

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	e.ServeHTTP(httptest.NewRecorder(), req)
	assert.False(t, ok, "no X-Client-IP, no address on the context")
}

func TestUnaryClientInterceptor_WritesFromContextAndOverwritesCallerMetadata(t *testing.T) {
	var seen metadata.MD
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		seen, _ = metadata.FromOutgoingContext(ctx)
		return nil
	}
	ic := clientip.UnaryClientInterceptor()

	// Context has an address: it is sent, and a hand-written value is replaced.
	ctx := clientip.NewContext(context.Background(), netip.MustParseAddr("203.0.113.7"))
	ctx = metadata.AppendToOutgoingContext(ctx, clientip.MetadataKey, "6.6.6.6", "other", "keep")
	require.NoError(t, ic(ctx, "/x", nil, nil, nil, invoker))
	assert.Equal(t, []string{"203.0.113.7"}, seen.Get(clientip.MetadataKey))
	assert.Equal(t, []string{"keep"}, seen.Get("other"))

	// Context has none: nothing is sent, including a hand-written value.
	ctx = metadata.AppendToOutgoingContext(context.Background(), clientip.MetadataKey, "6.6.6.6")
	require.NoError(t, ic(ctx, "/x", nil, nil, nil, invoker))
	assert.Empty(t, seen.Get(clientip.MetadataKey))
}

func TestUnaryServerInterceptor_ReadsValidatedMetadata(t *testing.T) {
	var got netip.Addr
	var ok bool
	handler := func(ctx context.Context, _ any) (any, error) {
		got, ok = clientip.FromContext(ctx)
		return nil, nil
	}
	ic := clientip.UnaryServerInterceptor()

	in := func(kv ...string) context.Context {
		return metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
	}

	_, err := ic(in(clientip.MetadataKey, "203.0.113.7"), nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "203.0.113.7", got.String())

	for name, ctx := range map[string]context.Context{
		"absent":    context.Background(),
		"invalid":   in(clientip.MetadataKey, "nope"),
		"two":       in(clientip.MetadataKey, "203.0.113.7", clientip.MetadataKey, "198.51.100.1"),
		"too long":  in(clientip.MetadataKey, strings.Repeat("1", clientip.MaxLen+1)),
		"forwarded": in("x-forwarded-for", "203.0.113.7"),
	} {
		t.Run(name, func(t *testing.T) {
			ok = false
			_, err := ic(ctx, nil, &grpc.UnaryServerInfo{}, handler)
			require.NoError(t, err)
			assert.False(t, ok)
		})
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }

func TestStreamServerInterceptor_ReadsValidatedMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(clientip.MetadataKey, "2001:db8::1"))
	var got netip.Addr
	var ok bool
	handler := func(_ any, ss grpc.ServerStream) error {
		got, ok = clientip.FromContext(ss.Context())
		return nil
	}
	require.NoError(t, clientip.StreamServerInterceptor()(nil, fakeStream{ctx: ctx}, &grpc.StreamServerInfo{}, handler))
	require.True(t, ok)
	assert.Equal(t, "2001:db8::1", got.String())
}

func TestStreamClientInterceptor_WritesFromContext(t *testing.T) {
	var seen metadata.MD
	streamer := func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		seen, _ = metadata.FromOutgoingContext(ctx)
		return nil, nil
	}
	ctx := clientip.NewContext(context.Background(), netip.MustParseAddr("203.0.113.7"))
	_, err := clientip.StreamClientInterceptor()(ctx, &grpc.StreamDesc{}, nil, "/x", streamer)
	require.NoError(t, err)
	assert.Equal(t, []string{"203.0.113.7"}, seen.Get(clientip.MetadataKey))
}
