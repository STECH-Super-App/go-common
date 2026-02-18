package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID    = "X-User-ID"
	HeaderUserRoles = "X-User-Roles"
	HeaderUserName  = "X-User-Name"
	HeaderUserType  = "X-User-Type"
	HeaderTenantID  = "X-Tenant-ID"
	HeaderScope     = "X-Scope"
	HeaderPhone     = "X-Phone"
)
