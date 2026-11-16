package trustedheaders_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/STECH-Super-App/go-common/pkg/clientip"
	"github.com/STECH-Super-App/go-common/pkg/trustedheaders"
)

func canon(names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = http.CanonicalHeaderKey(n)
	}
	return out
}

func TestStripAtEdge_CarriesTheNamedHeaders(t *testing.T) {
	got := trustedheaders.StripAtEdge()

	// The spec §3.3 list, spelled out literally so a deletion from the package
	// cannot go unnoticed here.
	required := canon(
		"X-Real-IP", "X-Client-IP", "Forwarded", "X-Forwarded-For", "X-Forwarded-Port",
		"X-Forwarded-Prefix", "X-Forwarded-Host", "X-Forwarded-Proto", "baggage",
		// Release 2: ingress-nginx renames an untrusted client's X-Forwarded-For
		// to this one; the gateway used to pass it through.
		"X-Original-Forwarded-For",
	)
	assert.Subset(t, got, required)

	// The identity headers the auth middleware trusts today.
	assert.Subset(t, got, canon(
		auth.HeaderUserID, auth.HeaderUserRoles, auth.HeaderUserName, auth.HeaderUserType, auth.HeaderAvatar,
		auth.HeaderScope, auth.HeaderPhone, auth.HeaderTokenJTI, auth.HeaderAuthOutcome, auth.HeaderAuthAction,
		auth.HeaderAuthChecked, auth.HeaderDeviceFingerprint,
		authz.HeaderTenantMemberships, authz.HeaderTenantReadonlyMemberships, authz.HeaderTenantMembershipSince,
		clientip.Header,
		auth.HeaderRealIP, auth.HeaderForwardedFor,
	))

	// Retired headers stay stripped: an old client may still send them.
	assert.Subset(t, got, canon("X-Chat-Read-Context", "X-Team-Memberships"))
	// Trace context is overwritten by the gateway's own root span.
	assert.Subset(t, got, canon("traceparent", "tracestate"))
}

func TestClientAddress_IncludesXOriginalForwardedFor(t *testing.T) {
	assert.Contains(t, trustedheaders.ClientAddress(), http.CanonicalHeaderKey("X-Original-Forwarded-For"))
}

func TestStripAtEdge_IsCanonicalDeduplicatedAndACopy(t *testing.T) {
	got := trustedheaders.StripAtEdge()
	seen := map[string]bool{}
	for _, name := range got {
		assert.Equal(t, http.CanonicalHeaderKey(name), name, "canonical form, so Header.Del and a lookup agree")
		assert.False(t, seen[name], "%s listed twice", name)
		seen[name] = true
	}

	got[0] = "Mutated"
	assert.NotEqual(t, "Mutated", trustedheaders.StripAtEdge()[0], "callers get a copy")
}

func TestStrip_RemovesEveryListedHeaderAndNothingElse(t *testing.T) {
	h := http.Header{}
	for _, name := range trustedheaders.StripAtEdge() {
		h.Add(name, "forged")
		h.Add(name, "forged-again")
	}
	h.Set("Authorization", "Bearer x")
	h.Set(auth.HeaderDeviceID, "device")
	h.Set("X-Request-Id", "rid")

	trustedheaders.Strip(h)

	assert.Equal(t, http.Header{
		"Authorization": {"Bearer x"},
		"X-Device-Id":   {"device"},
		"X-Request-Id":  {"rid"},
	}, h)
}

func TestStrip_MatchesCaseInsensitively(t *testing.T) {
	h := http.Header{}
	// Non-canonical keys written straight into the map, as a raw proxy might.
	h["x-forwarded-for"] = []string{"1.1.1.1"}
	h["BAGGAGE"] = []string{"k=v"}
	trustedheaders.Strip(h)
	assert.Empty(t, h)
}

// clientSupplied lists the Header* constants of pkg/auth and pkg/authz that are
// NOT gateway-asserted: a client legitimately sends them, or they are response
// headers or pure names. Everything else in those two packages is a header a
// service trusts, so it MUST be in the strip list — this test reads the two
// packages' source, so a new header constant fails here until it is classified.
var clientSupplied = map[string]bool{
	"X-Device-Id":        true, // the client's own device id, validated not asserted
	"User-Agent":         true,
	"X-Acting-Tenant-Id": true, // a client selector, never proof (auth.ParseActingTenantID)
}

func TestEveryTrustedHeaderConstantIsClassified(t *testing.T) {
	strip := map[string]bool{}
	for _, n := range trustedheaders.StripAtEdge() {
		strip[n] = true
	}

	for _, dir := range []string{"../auth", "../authz"} {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, nil, 0)
		require.NoError(t, err)
		for _, pkg := range pkgs {
			for fileName, file := range pkg.Files {
				if strings.HasSuffix(fileName, "_test.go") {
					continue
				}
				for _, decl := range file.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}
					for _, spec := range gen.Specs {
						vs := spec.(*ast.ValueSpec)
						for i, name := range vs.Names {
							if !strings.HasPrefix(name.Name, "Header") || i >= len(vs.Values) {
								continue
							}
							lit, ok := vs.Values[i].(*ast.BasicLit)
							if !ok || lit.Kind != token.STRING {
								continue
							}
							value, err := strconv.Unquote(lit.Value)
							require.NoError(t, err)
							canonical := http.CanonicalHeaderKey(value)
							if clientSupplied[canonical] {
								assert.False(t, strip[canonical], "%s is client-supplied and must not be stripped", canonical)
								continue
							}
							assert.True(t, strip[canonical],
								"%s.%s = %q is a header a service trusts: add it to trustedheaders (or to clientSupplied in this test, with a reason)",
								pkg.Name, name.Name, value)
						}
					}
				}
			}
		}
	}
}
