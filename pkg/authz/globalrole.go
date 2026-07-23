package authz

import (
	"strings"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

// GlobalRole is a platform-wide role carried in the JWT `roles` claim and the
// X-User-Roles header. It is distinct from the per-tenant role
// (ADMIN/MANAGER/OPERATOR) carried in TenantMembership.Role, which is keyed per
// tenant membership. A higher GlobalRole satisfies a lower GlobalRole *gate*,
// but confers no per-tenant authority.
type GlobalRole string

// Global roles, ordered by authority in globalRoleRank below.
const (
	GlobalRoleUser         GlobalRole = "user"
	GlobalRoleSupportAgent GlobalRole = "support_agent"
	GlobalRoleAdmin        GlobalRole = "admin"
)

// globalRoleRank orders global roles for the >= comparison: admin ⊇ support_agent ⊇ user.
var globalRoleRank = map[GlobalRole]int{
	GlobalRoleUser:         1,
	GlobalRoleSupportAgent: 2,
	GlobalRoleAdmin:        3,
}

// HasMinGlobalRole reports whether any role in the comma-separated rolesCSV
// ranks >= minRole in the platform hierarchy admin > support_agent > user. It is
// THE global-role authorization predicate every Go service routes through.
// Unknown role strings rank 0 and never satisfy a gate; an unknown minRole returns
// false (fail-closed).
func HasMinGlobalRole(rolesCSV string, minRole GlobalRole) bool {
	minRank, ok := globalRoleRank[minRole]
	if !ok {
		return false
	}
	for _, raw := range strings.Split(rolesCSV, ",") {
		if rank, ok := globalRoleRank[GlobalRole(strings.TrimSpace(raw))]; ok && rank >= minRank {
			return true
		}
	}
	return false
}

// CallerHasMinGlobalRole reads the caller's X-User-Roles value from the echo
// context (populated by AuthMiddleware) and reports whether it meets minRole.
func CallerHasMinGlobalRole(c echo.Context, minRole GlobalRole) bool {
	raw, _ := c.Get(string(auth.ContextKeyUserRoles)).(string)
	return HasMinGlobalRole(raw, minRole)
}

// IsValidGlobalRole reports whether s is an assignable global role. It is the
// canonical valid-role check; user-service uses it to validate role mutations.
func IsValidGlobalRole(s string) bool {
	_, ok := globalRoleRank[GlobalRole(s)]
	return ok
}
