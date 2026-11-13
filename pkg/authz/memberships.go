// Package authz provides authorization helpers shared across services. It is
// distinct from pkg/auth (authentication primitives — claims, headers, context
// keys). Its concerns are: the X-Tenant-Memberships header codec
// (ParseTenantMemberships / FormatTenantMemberships) — the fleet-wide single
// parser/formatter of a caller's tenant roles — the two chat-only companion
// headers (X-Tenant-Membership-Since, membership_since.go; and
// X-Tenant-Readonly-Memberships, readonly_memberships.go) with the one
// compact-to-long role expansion they share with the gateway (tenant_roles.go),
// and the global-role predicate (HasMinGlobalRole, see globalrole.go).
// Per-tenant access decisions are made by each service against the parsed
// memberships (or via tenant-service), not by a shared helper here.
package authz

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/labstack/echo/v4"
)

// ReasonTenantMembershipsInvalid is the typed 400 of ParseTenantMembershipsStrict.
const ReasonTenantMembershipsInvalid = "COMMON_TENANT_MEMBERSHIPS_INVALID"

// HeaderTenantMemberships carries the caller's tenant roles as a comma-separated
// list like "<tenant_id>=ADMIN,<tenant_id>=MANAGER". The API Gateway builds it
// from the JWT's tenant claims (see auth.TenantClaim) and downstream services
// read it via this package. This codec is the ONLY parser/formatter of the
// header fleet-wide — never split the string by hand.
const HeaderTenantMemberships = "X-Tenant-Memberships"

// TenantMembership is a caller's role in a single tenant. Ownership is keyed on
// the bare tenant id — the pre-unification (owner_type, owner_id) pair is gone.
type TenantMembership struct {
	TenantID string
	Role     string
}

// ParseTenantMemberships decodes the gateway's X-Tenant-Memberships header.
// Format: "<tenant_id>=ADMIN,<tenant_id>=MANAGER". An empty header yields nil.
// Malformed entries (no '=', empty tenant id, or empty role) are skipped; when
// every entry is malformed the result is nil. Order is preserved. O(n) over the
// header length, one slice allocation. Role values are not validated here — the
// header is trusted (gateway-set) and role vocabulary lives with the consumer.
//
// This lenient reader is kept for compatibility: it accepts a tenant id that is
// not a UUID, a repeated tenant, and reads past a malformed entry. New code
// should use ParseTenantMembershipsStrict, the same strictness the two chat
// header codecs already have.
func ParseTenantMemberships(header string) []TenantMembership {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}

	entries := strings.Split(header, ",")
	out := make([]TenantMembership, 0, len(entries))
	for _, raw := range entries {
		m, ok := parseTenantEntry(raw)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseTenantEntry(raw string) (TenantMembership, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TenantMembership{}, false
	}
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 || eq == len(raw)-1 {
		// eq == -1: no '='. eq == 0: empty tenant id. eq == last: empty role.
		return TenantMembership{}, false
	}
	return TenantMembership{TenantID: raw[:eq], Role: raw[eq+1:]}, true
}

// ParseTenantMembershipsStrict decodes X-Tenant-Memberships strictly, like
// ParseMembershipSince: every entry must be "<uuid>=<role>", the tenant id is
// lower-cased and may not be the nil UUID or repeat, the role is non-empty and
// holds no separator, whitespace or control character, no entry may be empty,
// and at most MaxMembershipHeaderEntries are accepted. Any violation rejects the
// whole header with a typed 400 COMMON_TENANT_MEMBERSHIPS_INVALID and returns no
// partial result. An empty header is an empty result. The role vocabulary is not
// checked (an unknown role passes through, so a future mint stays compatible).
func ParseTenantMembershipsStrict(header string) ([]TenantMembership, error) {
	entries, ok := splitMembershipEntries(header)
	if !ok {
		return nil, invalidTenantMemberships()
	}
	out := make([]TenantMembership, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		fields := strings.Split(raw, "=")
		if len(fields) != 2 {
			return nil, invalidTenantMemberships()
		}
		id, ok := membershipTenantID(fields[0])
		if !ok || !validMembershipRole(fields[1]) {
			return nil, invalidTenantMemberships()
		}
		if _, dup := seen[id]; dup {
			return nil, invalidTenantMemberships()
		}
		seen[id] = struct{}{}
		out = append(out, TenantMembership{TenantID: id, Role: fields[1]})
	}
	return out, nil
}

func invalidTenantMemberships() error {
	return commonerr.New(http.StatusBadRequest).
		Reason(ReasonTenantMembershipsInvalid).
		Message("Invalid tenant memberships header").
		Build()
}

// validMembershipRole: non-empty, no ',' or '=' and no whitespace or control
// rune — what a value needs to survive a round trip through the header.
func validMembershipRole(role string) bool {
	return role != "" && !strings.ContainsFunc(role, func(r rune) bool {
		return r == ',' || r == '=' || unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// FormatTenantMemberships is the inverse of ParseTenantMemberships: it renders
// memberships back into the header wire form. An empty or nil slice yields "".
//
// An entry whose id or role is empty or holds a ',' or '=' is omitted: written
// out it would be read back as a different entry — a role "ADMIN,<other id>=ADMIN"
// would forge a second membership. One bad entry costs only itself, as in the
// chat header formatters.
func FormatTenantMemberships(ms []TenantMembership) string {
	if len(ms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		if !formattable(m.TenantID) || !formattable(m.Role) {
			continue
		}
		parts = append(parts, m.TenantID+"="+m.Role)
	}
	return strings.Join(parts, ",")
}

// formattable: non-empty and free of the two header separators.
func formattable(v string) bool {
	return v != "" && !strings.ContainsAny(v, ",=")
}

// TenantMembershipsFromContext retrieves the memberships that AuthMiddleware
// parsed from the X-Tenant-Memberships header. Returns (nil, false) when the
// middleware has not run; (nil, true) when it ran but no header was present.
func TenantMembershipsFromContext(c echo.Context) ([]TenantMembership, bool) {
	v := c.Get(string(auth.ContextKeyTenantMemberships))
	if v == nil {
		return nil, false
	}
	m, ok := v.([]TenantMembership)
	if !ok {
		return nil, false
	}
	return m, true
}
