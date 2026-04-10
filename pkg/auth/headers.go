package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID    = "X-User-ID"
	HeaderUserRoles = "X-User-Roles"
	HeaderUserName  = "X-User-Name"
	HeaderUserType  = "X-User-Type"
	HeaderTenants   = "X-Tenants"
	HeaderAvatar    = "X-Avatar"

	HeaderScope        = "X-Scope"
	HeaderPhone        = "X-Phone"
	HeaderTokenJTI     = "X-Token-JTI"
	HeaderTokenRevoked = "X-Token-Revoked" // Request header: gateway → service, set when JTI is in blacklist
	HeaderTokenStatus  = "X-Token-Status"  // Response header: gateway → client, value "revoked" signals refresh needed
	HeaderAuthChecked  = "X-Auth-Checked"
	HeaderDeviceID     = "X-Device-Id"
	HeaderUserAgent    = "User-Agent"
	HeaderRealIP       = "X-Real-IP"
	HeaderForwardedFor = "X-Forwarded-For"
)
