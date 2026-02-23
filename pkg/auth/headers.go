package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID    = "X-User-ID"
	HeaderUserRoles = "X-User-Roles"
	HeaderUserName  = "X-User-Name"
	HeaderUserType  = "X-User-Type"
	HeaderTenants   = "X-Tenants"

	HeaderScope        = "X-Scope"
	HeaderPhone        = "X-Phone"
	HeaderDeviceID     = "X-Device-Id"
	HeaderUserAgent    = "User-Agent"
	HeaderRealIP       = "X-Real-IP"
	HeaderForwardedFor = "X-Forwarded-For"
)
