// Package trustedheaders is the one list of request headers a client must never
// be able to set on a request that reaches a service (audit-fix campaign spec
// §3.3, finding GW-04).
//
// A service trusts a handful of headers because api-gateway writes them from a
// verified token (X-User-ID, X-Tenant-Memberships, …) or from its own
// measurement (X-Client-IP, X-Device-Fingerprint). That trust is only as good
// as the gateway's promise to delete the client's copy first. The gateway used
// to keep its own list, which fell behind the headers go-common added (X-Real-IP
// was trusted and never stripped). Now the names live here, beside the code that
// trusts them:
//
//   - the gateway calls Strip (or ranges StripAtEdge) on every inbound request,
//     before it writes its own values;
//   - the gateway's test sets every name in StripAtEdge on a request and asserts
//     none survives, and asserts every header it sets is in the list;
//   - this package's test reads pkg/auth and pkg/authz and fails when a new
//     Header* constant there is in neither the list nor the client-supplied
//     exemptions, so a new trusted header cannot be added without being stripped.
//
// Headers a client legitimately sends are NOT here: X-Device-Id (validated, not
// asserted), X-User-Locale, X-Request-ID, X-Acting-Tenant-Id (a selector, never
// proof), Authorization, Idempotency-Key, User-Agent.
//
// All names are returned in canonical form (net/http.CanonicalHeaderKey) and as
// a fresh slice, so a caller cannot edit the list for everyone else.
package trustedheaders

import (
	"net/http"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/STECH-Super-App/go-common/pkg/clientip"
)

// GatewayAsserted returns the identity and authentication headers only
// api-gateway may write: services read them as proof of who is calling.
func GatewayAsserted() []string {
	return canonical(
		auth.HeaderUserID,
		auth.HeaderUserRoles,
		auth.HeaderUserName,
		auth.HeaderUserType,
		auth.HeaderAvatar,
		auth.HeaderScope,
		auth.HeaderPhone,
		auth.HeaderTokenJTI,
		auth.HeaderAuthOutcome,
		auth.HeaderAuthAction,
		auth.HeaderAuthChecked,
		auth.HeaderDeviceFingerprint,
		authz.HeaderTenantMemberships,
		authz.HeaderTenantReadonlyMemberships,
		authz.HeaderTenantMembershipSince,
	)
}

// ClientAddress returns every header through which a caller can claim an
// address, plus the gateway's own X-Client-IP. The gateway deletes them all and
// then writes the one it established (clientip.Set).
func ClientAddress() []string {
	return canonical(
		clientip.Header,
		// Spelled out: auth.HeaderRealIP / HeaderForwardedFor are banned for
		// everyone, and this is the one place that must name them (the test
		// ties these literals to the constants).
		"X-Real-IP",
		"X-Forwarded-For",
		"Forwarded",
		"X-Forwarded-Port",
		"X-Forwarded-Prefix",
		"X-Forwarded-Host",
		"X-Forwarded-Proto",
		// What ingress-nginx names the client's own X-Forwarded-For when it
		// does not trust it (use-forwarded-headers: false): still caller-
		// supplied text, and the gateway used to pass it through (release 2).
		"X-Original-Forwarded-For",
	)
}

// TraceContext returns the W3C trace headers. A caller's traceparent must not
// choose whether a trace is sampled (the gateway starts its own root span, see
// middleware.WithUntrustedParent), and its baggage must not travel into outbox
// rows and Kafka messages (SL-24). The gateway strips them from the inbound
// request after its tracing middleware has read them and before it injects its
// own span's traceparent.
func TraceContext() []string {
	return canonical("traceparent", "tracestate", "baggage")
}

// Retired returns headers no service reads any more but an old client or an
// old gateway build may still send; they stay stripped so a future reader of
// the same name cannot be fed by a caller.
func Retired() []string {
	return canonical("X-Chat-Read-Context", "X-Team-Memberships")
}

// StripAtEdge is every header the gateway deletes from an inbound request: the
// union of the four lists above, de-duplicated.
func StripAtEdge() []string {
	var out []string
	seen := map[string]bool{}
	for _, list := range [][]string{GatewayAsserted(), ClientAddress(), TraceContext(), Retired()} {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// Strip deletes every StripAtEdge header from h, whatever case its key was
// stored in (a raw proxy can leave non-canonical keys in the map, which
// Header.Del would miss).
func Strip(h http.Header) {
	names := map[string]bool{}
	for _, name := range StripAtEdge() {
		names[name] = true
	}
	for key := range h {
		if names[http.CanonicalHeaderKey(key)] {
			delete(h, key)
		}
	}
}

func canonical(names ...string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = http.CanonicalHeaderKey(name)
	}
	return out
}
