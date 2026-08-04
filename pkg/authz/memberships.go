// Package authz provides authorization helpers shared across services. It is
// distinct from pkg/auth (authentication primitives — claims, headers, context
// keys). Its two concerns are: the X-Tenant-Memberships header codec
// (ParseTenantMemberships / FormatTenantMemberships) — the fleet-wide single
// parser/formatter of a caller's tenant roles — and the global-role predicate
// (HasMinGlobalRole, see globalrole.go). Per-tenant access decisions are made by
// each service against the parsed memberships (or via tenant-service), not by a
// shared helper here.
package authz

import (
	"strings"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

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

// FormatTenantMemberships is the inverse of ParseTenantMemberships: it renders
// memberships back into the header wire form. An empty or nil slice yields "".
func FormatTenantMemberships(ms []TenantMembership) string {
	if len(ms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.TenantID+"="+m.Role)
	}
	return strings.Join(parts, ",")
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
