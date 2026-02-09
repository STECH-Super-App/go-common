package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID   = "X-User-ID"
	HeaderUserRole = "X-User-Role"
	HeaderUserName = "X-User-Name"
	HeaderUserType = "X-User-Type"
	HeaderTenantID = "X-Tenant-ID"
)