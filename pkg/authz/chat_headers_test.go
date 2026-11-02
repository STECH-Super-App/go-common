package authz_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
	tenantC = "33333333-3333-3333-3333-333333333333"
)

func requireTyped400(t *testing.T, err error, reason string) {
	t.Helper()
	var app *commonerr.AppError
	require.True(t, errors.As(err, &app), "want a typed AppError, got %v", err)
	assert.Equal(t, http.StatusBadRequest, app.Code)
	assert.Equal(t, reason, app.Reason)
}

// gatewayMemberships mirrors how api-gateway turns token claims into the
// X-Tenant-Memberships value: skip entries without id or role, expand the
// compact role, format through the fleet codec.
func gatewayMemberships(tenants []auth.TenantClaim) string {
	ms := make([]authz.TenantMembership, 0, len(tenants))
	for _, t := range tenants {
		if t.ID == "" || t.R == "" {
			continue
		}
		ms = append(ms, authz.TenantMembership{TenantID: t.ID, Role: authz.ExpandTenantRole(t.R)})
	}
	return authz.FormatTenantMemberships(ms)
}

// X-Tenant-Memberships is what sale (PHP), order, machinery and the rest parse.
// Claims that now carry a start time must produce the same bytes as claims that
// do not; this golden string is the guarantee those services rely on.
func TestTenantMembershipsHeaderIsByteIdenticalWithStartTimes(t *testing.T) {
	const golden = tenantA + "=ADMIN," + tenantB + "=MANAGER," + tenantC + "=OPERATOR"

	withoutAt := []auth.TenantClaim{
		{ID: tenantA, R: "A", Kind: "organisation"},
		{ID: tenantB, R: "M"},
		{ID: tenantC, R: "O", Kind: "personal"},
	}
	withAt := []auth.TenantClaim{
		{ID: tenantA, R: "A", Kind: "organisation", At: 1767225600},
		{ID: tenantB, R: "M", At: 1767225601},
		{ID: tenantC, R: "O", Kind: "personal", At: 1767225602},
	}

	assert.Equal(t, golden, gatewayMemberships(withoutAt))
	assert.Equal(t, golden, gatewayMemberships(withAt))
	assert.Equal(t, golden, authz.FormatTenantMemberships([]authz.TenantMembership{
		{TenantID: tenantA, Role: "ADMIN"},
		{TenantID: tenantB, Role: "MANAGER"},
		{TenantID: tenantC, Role: "OPERATOR"},
	}))
}

func TestExpandTenantRole(t *testing.T) {
	assert.Equal(t, "ADMIN", authz.ExpandTenantRole("A"))
	assert.Equal(t, "MANAGER", authz.ExpandTenantRole("M"))
	assert.Equal(t, "OPERATOR", authz.ExpandTenantRole("O"))
	// A long-form or unknown code passes through verbatim.
	assert.Equal(t, "ADMIN", authz.ExpandTenantRole("ADMIN"))
	assert.Equal(t, "X", authz.ExpandTenantRole("X"))
	assert.Equal(t, "", authz.ExpandTenantRole(""))
}

func TestIsTenantRole(t *testing.T) {
	for _, r := range []string{"ADMIN", "MANAGER", "OPERATOR"} {
		assert.True(t, authz.IsTenantRole(r), r)
	}
	for _, r := range []string{"", "A", "admin", "OWNER", "ADMIN ", "SUPERADMIN"} {
		assert.False(t, authz.IsTenantRole(r), r)
	}
}

// ---- X-Tenant-Membership-Since ----

func TestHeaderNames(t *testing.T) {
	assert.Equal(t, "X-Tenant-Membership-Since", authz.HeaderTenantMembershipSince)
	assert.Equal(t, "X-Tenant-Readonly-Memberships", authz.HeaderTenantReadonlyMemberships)
}

func TestFormatMembershipSince(t *testing.T) {
	got := authz.FormatMembershipSince([]auth.TenantClaim{
		{ID: tenantA, R: "A", At: 1767225600},
		{ID: tenantB, R: "M", At: 0}, // unknown start: omitted, never a zero baseline
		{ID: tenantC, R: "O", Kind: "personal", At: 1767225602},
	})
	assert.Equal(t, tenantA+"=1767225600,"+tenantC+"=1767225602", got)
}

func TestFormatMembershipSinceNothingToForward(t *testing.T) {
	assert.Equal(t, "", authz.FormatMembershipSince(nil))
	assert.Equal(t, "", authz.FormatMembershipSince([]auth.TenantClaim{{ID: tenantA, R: "A"}}))
}

func TestMembershipSinceRoundTrip(t *testing.T) {
	claims := []auth.TenantClaim{
		{ID: tenantA, R: "A", At: 1767225600},
		{ID: tenantB, R: "M", At: 1767225601},
	}
	got, err := authz.ParseMembershipSince(authz.FormatMembershipSince(claims))
	require.NoError(t, err)
	assert.Equal(t, map[string]time.Time{
		tenantA: time.Unix(1767225600, 0).UTC(),
		tenantB: time.Unix(1767225601, 0).UTC(),
	}, got)
}

func TestParseMembershipSinceEmptyHeader(t *testing.T) {
	for _, h := range []string{"", "   "} {
		got, err := authz.ParseMembershipSince(h)
		require.NoError(t, err, "%q", h)
		assert.Empty(t, got)
	}
}

func TestParseMembershipSinceNormalizesTenantID(t *testing.T) {
	got, err := authz.ParseMembershipSince("AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA=5")
	require.NoError(t, err)
	assert.Equal(t, map[string]time.Time{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa": time.Unix(5, 0).UTC()}, got)
}

func TestParseMembershipSinceAcceptsZeroAndTheMaximum(t *testing.T) {
	got, err := authz.ParseMembershipSince(tenantA + "=0," + tenantB + "=253402300799")
	require.NoError(t, err)
	assert.Equal(t, time.Unix(0, 0).UTC(), got[tenantA])
	assert.Equal(t, time.Unix(253402300799, 0).UTC(), got[tenantB])
}

func TestParseMembershipSinceRejects(t *testing.T) {
	cases := map[string]string{
		"no equals":             tenantA,
		"empty tenant":          "=5",
		"empty value":           tenantA + "=",
		"extra field":           tenantA + "=ADMIN=5",
		"not a uuid":            "t1=5",
		"nil uuid":              "00000000-0000-0000-0000-000000000000=5",
		"braced uuid":           "{" + tenantA + "}=5",
		"negative":              tenantA + "=-1",
		"not a number":          tenantA + "=abc",
		"fractional":            tenantA + "=1.5",
		"plus sign":             tenantA + "=+5",
		"space in value":        tenantA + "= 5",
		"above year 9999":       tenantA + "=253402300800",
		"beyond int64":          tenantA + "=99999999999999999999",
		"duplicate":             tenantA + "=5," + tenantA + "=6",
		"duplicate case-folded": tenantA + "=5," + strings.ToUpper(tenantA) + "=6",
		"empty entry":           tenantA + "=5,,",
		"leading comma":         "," + tenantA + "=5",
		"trailing comma":        tenantA + "=5,",
		"one bad among good":    tenantA + "=5,junk," + tenantB + "=6",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := authz.ParseMembershipSince(header)
			requireTyped400(t, err, authz.ReasonMembershipSinceInvalid)
			assert.Nil(t, got, "no partial result")
		})
	}
}

func TestParseMembershipSinceBoundsEntries(t *testing.T) {
	build := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d=5", i+1)
		}
		return strings.Join(parts, ",")
	}
	got, err := authz.ParseMembershipSince(build(authz.MaxMembershipHeaderEntries))
	require.NoError(t, err)
	assert.Len(t, got, authz.MaxMembershipHeaderEntries)

	_, err = authz.ParseMembershipSince(build(authz.MaxMembershipHeaderEntries + 1))
	requireTyped400(t, err, authz.ReasonMembershipSinceInvalid)
}

// Format only emits what Parse accepts, so a bad claim costs its own seat and
// never turns the whole header into a 400 for the caller.
func TestFormatMembershipSinceOutputAlwaysParses(t *testing.T) {
	claims := []auth.TenantClaim{
		{ID: "t1", R: "A", At: 5},                           // not a UUID
		{ID: "00000000-0000-0000-0000-000000000000", At: 5}, // nil UUID
		{ID: "", R: "A", At: 5},                             // no id
		{ID: tenantA, R: "A", At: -3},                       // negative
		{ID: tenantA, R: "A", At: 253402300800},             // out of range
		{ID: strings.ToUpper(tenantB), R: "A", At: 7},       // canonicalized
		{ID: tenantB, R: "M", At: 8},                        // duplicate: first wins
		{ID: "a=b,c", R: "A", At: 5},                        // separators in the id
		{ID: tenantC, R: "O", At: 9},
	}
	header := authz.FormatMembershipSince(claims)
	assert.Equal(t, tenantB+"=7,"+tenantC+"=9", header)
	_, err := authz.ParseMembershipSince(header)
	require.NoError(t, err)
}

func TestFormatMembershipSinceCapsEntries(t *testing.T) {
	claims := make([]auth.TenantClaim, authz.MaxMembershipHeaderEntries+5)
	for i := range claims {
		claims[i] = auth.TenantClaim{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), R: "A", At: 5}
	}
	got, err := authz.ParseMembershipSince(authz.FormatMembershipSince(claims))
	require.NoError(t, err)
	assert.Len(t, got, authz.MaxMembershipHeaderEntries)
}

// ---- X-Tenant-Readonly-Memberships ----

func TestFormatReadonlyMembershipsExpandsRoles(t *testing.T) {
	got := authz.FormatReadonlyMemberships([]auth.TenantClaim{
		{ID: tenantA, R: "A", At: 1767225600},
		{ID: tenantB, R: "M", At: 1767225601},
		{ID: tenantC, R: "O", Kind: "personal", At: 1767225602},
	})
	assert.Equal(t,
		tenantA+"=ADMIN=1767225600,"+tenantB+"=MANAGER=1767225601,"+tenantC+"=OPERATOR=1767225602",
		got)
}

func TestFormatReadonlyMembershipsKeepsLongRoles(t *testing.T) {
	got := authz.FormatReadonlyMemberships([]auth.TenantClaim{{ID: tenantA, R: "MANAGER", At: 5}})
	assert.Equal(t, tenantA+"=MANAGER=5", got)
}

func TestFormatReadonlyMembershipsNothingToForward(t *testing.T) {
	assert.Equal(t, "", authz.FormatReadonlyMemberships(nil))
	assert.Equal(t, "", authz.FormatReadonlyMemberships([]auth.TenantClaim{}))
}

func TestReadonlyMembershipsRoundTrip(t *testing.T) {
	claims := []auth.TenantClaim{
		{ID: tenantA, R: "A", At: 1767225600},
		{ID: tenantB, R: "O", At: 1767225601},
	}
	got, err := authz.ParseReadonlyMemberships(authz.FormatReadonlyMemberships(claims))
	require.NoError(t, err)
	assert.Equal(t, []authz.ReadonlyMembership{
		{TenantID: tenantA, Role: "ADMIN", Since: time.Unix(1767225600, 0).UTC()},
		{TenantID: tenantB, Role: "OPERATOR", Since: time.Unix(1767225601, 0).UTC()},
	}, got)
}

func TestParseReadonlyMembershipsEmptyHeader(t *testing.T) {
	for _, h := range []string{"", "  "} {
		got, err := authz.ParseReadonlyMemberships(h)
		require.NoError(t, err, "%q", h)
		assert.Empty(t, got)
	}
}

func TestParseReadonlyMembershipsRejects(t *testing.T) {
	cases := map[string]string{
		"no equals":              tenantA,
		"two fields":             tenantA + "=ADMIN",
		"four fields":            tenantA + "=ADMIN=5=6",
		"empty role":             tenantA + "==5",
		"empty value":            tenantA + "=ADMIN=",
		"unknown role":           tenantA + "=OWNER=5",
		"compact role":           tenantA + "=A=5",
		"lower-case role":        tenantA + "=admin=5",
		"not a uuid":             "t1=ADMIN=5",
		"nil uuid":               "00000000-0000-0000-0000-000000000000=ADMIN=5",
		"negative":               tenantA + "=ADMIN=-1",
		"not a number":           tenantA + "=ADMIN=abc",
		"above year 9999":        tenantA + "=ADMIN=253402300800",
		"duplicate":              tenantA + "=ADMIN=5," + tenantA + "=MANAGER=6",
		"duplicate case-folded":  tenantA + "=ADMIN=5," + strings.ToUpper(tenantA) + "=ADMIN=5",
		"empty entry":            tenantA + "=ADMIN=5,,",
		"trailing comma":         tenantA + "=ADMIN=5,",
		"one bad among good":     tenantA + "=ADMIN=5,junk," + tenantB + "=ADMIN=5",
		"since-style entry only": tenantA + "=5",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := authz.ParseReadonlyMemberships(header)
			requireTyped400(t, err, authz.ReasonReadonlyMembershipsInvalid)
			assert.Nil(t, got, "no partial result")
		})
	}
}

func TestParseReadonlyMembershipsBoundsEntries(t *testing.T) {
	build := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d=ADMIN=5", i+1)
		}
		return strings.Join(parts, ",")
	}
	got, err := authz.ParseReadonlyMemberships(build(authz.MaxMembershipHeaderEntries))
	require.NoError(t, err)
	assert.Len(t, got, authz.MaxMembershipHeaderEntries)

	_, err = authz.ParseReadonlyMemberships(build(authz.MaxMembershipHeaderEntries + 1))
	requireTyped400(t, err, authz.ReasonReadonlyMembershipsInvalid)
}

func TestFormatReadonlyMembershipsOutputAlwaysParses(t *testing.T) {
	claims := []auth.TenantClaim{
		{ID: "t1", R: "A", At: 5},                     // not a UUID
		{ID: tenantA, R: "X", At: 5},                  // unknown role
		{ID: tenantA, R: "", At: 5},                   // no role
		{ID: tenantA, R: "A", At: 0},                  // no start time: no seat is forwarded
		{ID: tenantA, R: "A", At: -1},                 // negative
		{ID: strings.ToUpper(tenantB), R: "M", At: 7}, // canonicalized
		{ID: tenantB, R: "A", At: 8},                  // duplicate: first wins
		{ID: tenantC, R: "O", At: 9},
	}
	header := authz.FormatReadonlyMemberships(claims)
	assert.Equal(t, tenantB+"=MANAGER=7,"+tenantC+"=OPERATOR=9", header)
	_, err := authz.ParseReadonlyMemberships(header)
	require.NoError(t, err)
}

func TestFormatReadonlyMembershipsCapsEntries(t *testing.T) {
	claims := make([]auth.TenantClaim, authz.MaxMembershipHeaderEntries+5)
	for i := range claims {
		claims[i] = auth.TenantClaim{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), R: "A", At: 5}
	}
	got, err := authz.ParseReadonlyMemberships(authz.FormatReadonlyMemberships(claims))
	require.NoError(t, err)
	assert.Len(t, got, authz.MaxMembershipHeaderEntries)
}

// A suspended seat travels only in the read-only header; the active header
// never carries it.
func TestReadonlySeatsDoNotLeakIntoActiveHeaders(t *testing.T) {
	active := []auth.TenantClaim{{ID: tenantA, R: "A", At: 5}}
	readonly := []auth.TenantClaim{{ID: tenantB, R: "M", At: 6}}

	assert.NotContains(t, gatewayMemberships(active), tenantB)
	assert.NotContains(t, authz.FormatMembershipSince(active), tenantB)
	assert.NotContains(t, authz.FormatReadonlyMemberships(readonly), tenantA)
}
