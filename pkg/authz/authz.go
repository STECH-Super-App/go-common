// Package authz provides authorization helpers for cross-service team-role checks.
// It is distinct from pkg/auth (which handles authentication primitives — claims,
// headers, context keys) and is intended to be called from handlers to decide
// whether the current caller can act on a resource owned by a particular entity.
package authz

import (
	"strings"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

// OwnerType identifies the kind of entity that owns a resource.
type OwnerType string

// Owner kinds — the owning entity classifications authz understands.
const (
	OwnerUser   OwnerType = "USER"
	OwnerTenant OwnerType = "TENANT"
)

// Role is a membership role inside a team.
type Role string

// Team roles — ordered by authority in roleRank below.
const (
	RoleAdmin    Role = "ADMIN"
	RoleManager  Role = "MANAGER"
	RoleOperator Role = "OPERATOR"
)

// roleRank orders roles for the >= comparison inside CanAccess.
var roleRank = map[Role]int{
	RoleOperator: 1,
	RoleManager:  2,
	RoleAdmin:    3,
}

// Membership is a caller's role in a team, keyed by (OwnerType, OwnerID).
// team_id is deliberately omitted — authz decisions always key on the owner,
// not the team id.
type Membership struct {
	OwnerType OwnerType
	OwnerID   string
	Role      Role
}

// Memberships is the parsed list extracted from the gateway header.
type Memberships []Membership

const globalAdminRole = "admin"

// Parse decodes the gateway's X-Team-Memberships header.
// Format: "TENANT:<uuid>=MANAGER,USER:<uuid>=ADMIN".
// Malformed entries are silently skipped.
func Parse(header string) Memberships {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}

	entries := strings.Split(header, ",")
	out := make(Memberships, 0, len(entries))
	for _, raw := range entries {
		m, ok := parseEntry(raw)
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

func parseEntry(raw string) (Membership, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Membership{}, false
	}
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 || eq == len(raw)-1 {
		return Membership{}, false
	}
	lhs, roleStr := raw[:eq], raw[eq+1:]
	colon := strings.IndexByte(lhs, ':')
	if colon <= 0 || colon == len(lhs)-1 {
		return Membership{}, false
	}
	ownerType := OwnerType(lhs[:colon])
	if ownerType != OwnerUser && ownerType != OwnerTenant {
		return Membership{}, false
	}
	role := Role(roleStr)
	if _, ok := roleRank[role]; !ok {
		return Membership{}, false
	}
	return Membership{
		OwnerType: ownerType,
		OwnerID:   lhs[colon+1:],
		Role:      role,
	}, true
}

// MembershipsFromContext retrieves memberships previously set by AuthMiddleware.
// Returns (nil, false) when middleware has not run or no header was present.
func MembershipsFromContext(c echo.Context) (Memberships, bool) {
	v := c.Get(string(auth.ContextKeyTeamMemberships))
	if v == nil {
		return nil, false
	}
	m, ok := v.(Memberships)
	if !ok {
		return nil, false
	}
	return m, true
}

// CanAccess returns true when either
//   (a) the caller carries the global "admin" role in X-User-Roles, or
//   (b) the caller has a Membership matching (ownerType, ownerID) with Role
//       rank >= minRole rank.
//
// Role rank is ADMIN(3) > MANAGER(2) > OPERATOR(1).
func CanAccess(c echo.Context, ownerType OwnerType, ownerID string, minRole Role) bool {
	if hasGlobalAdmin(c) {
		return true
	}
	memberships, _ := MembershipsFromContext(c)
	minRank, ok := roleRank[minRole]
	if !ok {
		return false
	}
	for _, m := range memberships {
		if m.OwnerType == ownerType && m.OwnerID == ownerID {
			if rank, ok := roleRank[m.Role]; ok && rank >= minRank {
				return true
			}
		}
	}
	return false
}

// OwnerTypeFromBool bridges legacy is_tenant flags (e.g. on machinery records)
// to the typed OwnerType value expected by CanAccess.
func OwnerTypeFromBool(isTenant bool) OwnerType {
	if isTenant {
		return OwnerTenant
	}
	return OwnerUser
}

func hasGlobalAdmin(c echo.Context) bool {
	raw, _ := c.Get(string(auth.ContextKeyUserRoles)).(string)
	if raw == "" {
		return false
	}
	for _, r := range strings.Split(raw, ",") {
		if strings.TrimSpace(r) == globalAdminRole {
			return true
		}
	}
	return false
}
