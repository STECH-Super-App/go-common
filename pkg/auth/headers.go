package auth

// Header keys for user authentication data forwarded by the API Gateway.
const (
	HeaderUserID    = "X-User-ID"
	HeaderUserRoles = "X-User-Roles"
	HeaderUserName  = "X-User-Name"
	HeaderUserType  = "X-User-Type"
	HeaderAvatar    = "X-Avatar"

	// The caller's tenant roles travel in the X-Tenant-Memberships header. Its
	// name and codec live in go-common/pkg/authz (HeaderTenantMemberships,
	// ParseTenantMemberships / FormatTenantMemberships) — the fleet-wide single
	// parser/formatter — not here, to keep that concern in one package.

	HeaderScope    = "X-Scope"
	HeaderPhone    = "X-Phone"
	HeaderTokenJTI = "X-Token-JTI" //nolint:gosec // Request header: gateway → service, the verified token's JTI.

	// HeaderAuthOutcome is the internal request header the gateway sets to the
	// classified token state (see signal.go Outcome* values). Trusted: stripped
	// from inbound client requests at the gateway boundary.
	HeaderAuthOutcome = "X-Auth-Outcome" //nolint:gosec // Request header: gateway → service.
	// HeaderAuthAction is the response header telling the client what to do on an
	// auth failure: "refresh" or "reauth" (see signal.go Action* values).
	HeaderAuthAction  = "X-Auth-Action" //nolint:gosec // Response header: service → client.
	HeaderAuthChecked = "X-Auth-Checked"
	HeaderDeviceID    = "X-Device-Id"
	// HeaderDeviceFingerprint is a server-asserted sha256(UA + IP) fingerprint
	// injected by api-gateway on every forwarded request. It is always overwritten
	// at the gateway boundary — never trust a value of this header that arrives
	// from outside the gateway.
	HeaderDeviceFingerprint = "X-Device-Fingerprint"
	HeaderUserAgent         = "User-Agent"
	HeaderRealIP            = "X-Real-IP"
	HeaderForwardedFor      = "X-Forwarded-For"
)
