package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID    = "X-User-ID"
	HeaderUserRoles = "X-User-Roles"
	HeaderUserName  = "X-User-Name"
	HeaderUserType  = "X-User-Type"
	HeaderAvatar    = "X-Avatar"

	// HeaderTeamMemberships carries the caller's team roles as a comma-separated
	// list like "TENANT:<uuid>=MANAGER,USER:<uuid>=ADMIN". Services read it via
	// the go-common/pkg/authz package.
	HeaderTeamMemberships = "X-Team-Memberships"

	HeaderScope        = "X-Scope"
	HeaderPhone        = "X-Phone"
	HeaderTokenJTI     = "X-Token-JTI"     //nolint:gosec // Not credentials — HTTP header names for token revocation signaling.
	HeaderTokenRevoked = "X-Token-Revoked" //nolint:gosec // Request header: gateway → service, set when JTI is in blacklist.
	HeaderTokenStatus  = "X-Token-Status"  //nolint:gosec // Response header: gateway → client, value "revoked" signals refresh needed.
	HeaderAuthChecked  = "X-Auth-Checked"
	HeaderDeviceID     = "X-Device-Id"
	HeaderUserAgent    = "User-Agent"
	HeaderRealIP       = "X-Real-IP"
	HeaderForwardedFor = "X-Forwarded-For"
)
