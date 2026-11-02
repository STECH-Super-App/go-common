package authz

// Tenant membership roles in the long form every header consumer authorizes
// on (sale canActAs, order actingRoleFor, chat's operator contact-person check).
const (
	TenantRoleAdmin    = "ADMIN"
	TenantRoleManager  = "MANAGER"
	TenantRoleOperator = "OPERATOR"
)

// compactTenantRoles maps the role codes auth-service mints into the JWT
// ("A"/"M"/"O" keep the token small) to the long form.
var compactTenantRoles = map[string]string{
	"A": TenantRoleAdmin,
	"M": TenantRoleManager,
	"O": TenantRoleOperator,
}

// ExpandTenantRole expands a JWT claim's compact role code into the long-form
// role every membership header carries. It is the ONE expansion fleet-wide: the
// gateway uses it for X-Tenant-Memberships and this package uses it for the
// chat-only headers, so the three can never disagree. An unknown code passes
// through verbatim, so a future long-form mint stays compatible.
func ExpandTenantRole(role string) string {
	if long, ok := compactTenantRoles[role]; ok {
		return long
	}
	return role
}

// IsTenantRole reports whether role is one of the three long-form roles.
func IsTenantRole(role string) bool {
	switch role {
	case TenantRoleAdmin, TenantRoleManager, TenantRoleOperator:
		return true
	default:
		return false
	}
}
