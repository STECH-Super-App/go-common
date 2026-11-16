// Package clientip is the one way a service learns the address of the person
// calling it (audit-fix campaign spec §3.3).
//
// # The rule
//
// api-gateway is the single authority. It takes the address from the ingress
// only, deletes every incoming address header and writes exactly one of its own,
// X-Client-IP (Set). A service reads only that header, through this package.
// Nothing else is believed:
//
//   - not X-Forwarded-For, X-Real-IP or Forwarded — a client types those;
//   - not the TCP peer — behind ingress it is the ingress pod, and "the peer"
//     is a different answer on every deployment;
//   - not an echo IPExtractor — httpserver.New fixes it to the peer on purpose.
//
// "No address" is an answer, not an error: FromRequest, FromEcho and
// FromContext return (zero, false) and the CALLER decides — a login that must
// record an address refuses, an anonymous view counter counts it as unknown. No
// function here ever falls back to the peer, because a silent fallback is how a
// shared ingress address ended up keying every rate limit in one bucket.
//
// # Passing it on
//
// A service that calls another on a user's behalf forwards the address in gRPC
// metadata: Middleware puts the validated address on the request context,
// UnaryClientInterceptor / StreamClientInterceptor write it from the context,
// and UnaryServerInterceptor / StreamServerInterceptor read it back into the
// context of the called service. Attach them next to requestid's pair.
// Backend-to-backend calls are trusted by network position (no service tokens,
// Critical Rule 6), so the server side accepts whatever a caller sends — but it
// validates the value like any input, and the client side overwrites a
// hand-written value rather than appending to it.
//
// # Writing it
//
// Set is reserved for api-gateway. .golangci.strict.yml bans calling it
// anywhere else; the gateway marks its one call `//nolint:forbidigo`.
package clientip

import (
	"context"
	"net/http"
	"net/netip"

	"github.com/labstack/echo/v4"
)

const (
	// Header is the request header the gateway writes and services read.
	Header = "X-Client-IP"

	// MetadataKey is Header's gRPC metadata key (lower case, as gRPC requires).
	MetadataKey = "x-client-ip"

	// MaxLen is the longest text form of an address this package accepts: 45
	// bytes is an IPv6 address with an embedded IPv4 tail. A zone ("%eth0") is
	// refused outright, so it adds nothing. The cap is checked before parsing so
	// a hostile header costs nothing to reject.
	MaxLen = 45
)

// Parse validates raw as a client address and returns it in canonical form
// (lower-case IPv6, IPv4-mapped IPv6 unmapped to IPv4). It refuses an empty or
// over-long value, anything netip.ParseAddr does not accept as a bare address
// (a list, a host:port, a hostname, surrounding spaces), an IPv6 zone, and an
// unspecified or multicast address — none of those can be a caller.
// Loopback and private addresses are accepted: local development and a private
// NAT are real callers.
func Parse(raw string) (netip.Addr, bool) {
	if raw == "" || len(raw) > MaxLen {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsMulticast() {
		return netip.Addr{}, false
	}
	return addr, true
}

// FromHeader returns the validated X-Client-IP of h. It is absent when the
// header is missing, invalid, or present more than once: with two values there
// is no telling which one the gateway wrote.
func FromHeader(h http.Header) (netip.Addr, bool) {
	values := h.Values(Header)
	if len(values) != 1 {
		return netip.Addr{}, false
	}
	return Parse(values[0])
}

// FromRequest is FromHeader on r's headers. A nil request has no address.
func FromRequest(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	return FromHeader(r.Header)
}

// FromEcho is FromRequest for an Echo context.
func FromEcho(c echo.Context) (netip.Addr, bool) {
	return FromRequest(c.Request())
}

// Set makes addr the one X-Client-IP of h, replacing whatever was there. An
// invalid or unusable addr leaves NO header — never the client's own value.
//
// Reserved for api-gateway (see the package comment).
func Set(h http.Header, addr netip.Addr) {
	h.Del(Header)
	if !addr.IsValid() {
		return
	}
	if canonical, ok := Parse(addr.String()); ok {
		h.Set(Header, canonical.String())
	}
}

type ctxKey struct{}

// NewContext returns ctx carrying addr. An invalid address is not stored, so
// FromContext never reports one.
func NewContext(ctx context.Context, addr netip.Addr) context.Context {
	canonical, ok := Parse(addr.String())
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, canonical)
}

// FromContext returns the address NewContext, Middleware or a server
// interceptor put on ctx.
func FromContext(ctx context.Context) (netip.Addr, bool) {
	addr, ok := ctx.Value(ctxKey{}).(netip.Addr)
	return addr, ok
}

// Middleware puts the request's validated X-Client-IP on the request context so
// code below the handler (and the gRPC client interceptors) can use it without
// holding an echo.Context. A request without an address passes through
// unchanged. httpserver.New installs it.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if addr, ok := FromEcho(c); ok {
				c.SetRequest(c.Request().WithContext(NewContext(c.Request().Context(), addr)))
			}
			return next(c)
		}
	}
}
