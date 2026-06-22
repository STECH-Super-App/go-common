package authz

import (
	"strings"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

// GlobalRole is a platform-wide role carried in the JWT `roles` claim and the
// X-User-Roles header. It is distinct from the team-scoped Role
// (ADMIN/MANAGER/OPERATOR) defined in authz.go, which is keyed per team
// membership. A higher GlobalRole satisfies a lower GlobalRole *gate*, but
// confers no team-scoped authority.
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
// ranks >= min in the platform hierarchy admin > support_agent > user. It is
// THE global-role authorization predicate every Go service routes through.
// Unknown role strings rank 0 and never satisfy a gate; an unknown min returns
// false (fail-closed).
func HasMinGlobalRole(rolesCSV string, min GlobalRole) bool {
	minRank, ok := globalRoleRank[min]
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
// context (populated by AuthMiddleware) and reports whether it meets min.
func CallerHasMinGlobalRole(c echo.Context, min GlobalRole) bool {
	raw, _ := c.Get(string(auth.ContextKeyUserRoles)).(string)
	return HasMinGlobalRole(raw, min)
}

// IsValidGlobalRole reports whether s is an assignable global role. It is the
// canonical valid-role check; user-service uses it to validate role mutations.
func IsValidGlobalRole(s string) bool {
	_, ok := globalRoleRank[GlobalRole(s)]
	return ok
}
