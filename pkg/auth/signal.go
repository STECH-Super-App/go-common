package auth

// X-Auth-Action response-header values. The gateway/services tell the client
// which action to take when authentication fails.
const (
	ActionRefresh = "refresh" // access token can be renewed; refresh then retry
	ActionReauth  = "reauth"  // session is gone or never existed; sign in again
)

// X-Auth-Outcome internal request-header values (gateway → service). The gateway
// classifies the token's intrinsic state into one of these; AuthMiddleware maps
// it to the client-facing response. Never trust this header from outside the
// gateway — it is stripped from inbound requests at the gateway boundary.
const (
	OutcomeExpired     = "expired"       // signature valid, exp in the past
	OutcomeStale       = "stale"         // revoked because claims changed / rotated
	OutcomeLoggedOut   = "logged_out"    // revoked by logout
	OutcomeRevoked     = "revoked"       // revoked for security / unknown reason
	OutcomeSuspended   = "suspended"     // revoked by admin ban
	OutcomeUntrusted   = "untrusted"     // malformed / bad signature / wrong alg
	OutcomeNotYetValid = "not_yet_valid" // signature valid, nbf in the future
)

// Revocation reason constants stored in the blacklist by auth-service. The
// gateway classifies on these; keep auth-service and go-common in lockstep by
// using these constants on both sides.
const (
	RevokeReasonUserUpdated  = "user_updated"
	RevokeReasonTokenRefresh = "token_refresh"
	RevokeReasonLogout       = "logout"
	RevokeReasonTheft        = "theft"
	RevokeReasonAdminBan     = "admin_ban"
)

// ReasonAuthRequired is the i18n key for "no usable credential" — emitted for
// untrusted, not-yet-valid, and missing-token cases. Shared so the middleware
// and the outcome map never diverge.
const ReasonAuthRequired = "COMMON_AUTH_REQUIRED"

// BlacklistEntry is the subset of the revoked_token:{jti} JSON value the gateway
// needs. auth-service writes additional fields (jti, expires_at) that are
// ignored here.
type BlacklistEntry struct {
	Reason string `json:"reason"`
}

// Failure is the client-facing response for a failed auth outcome.
type Failure struct {
	Action  string // X-Auth-Action header value
	Reason  string // COMMON_* i18n key for the body
	Message string // English-only log/dev fallback
}

var outcomeResponses = map[string]Failure{
	OutcomeExpired:     {Action: ActionRefresh, Reason: "COMMON_TOKEN_EXPIRED", Message: "access token expired, please refresh your session"},
	OutcomeStale:       {Action: ActionRefresh, Reason: "COMMON_TOKEN_STALE", Message: "access token is out of date, please refresh your session"},
	OutcomeLoggedOut:   {Action: ActionReauth, Reason: "COMMON_SESSION_LOGGED_OUT", Message: "session ended, please sign in again"},
	OutcomeRevoked:     {Action: ActionReauth, Reason: "COMMON_SESSION_REVOKED", Message: "session is no longer valid, please sign in again"},
	OutcomeSuspended:   {Action: ActionReauth, Reason: "COMMON_ACCOUNT_SUSPENDED", Message: "account suspended, please contact support"},
	OutcomeUntrusted:   {Action: ActionReauth, Reason: ReasonAuthRequired, Message: "authentication required"},
	OutcomeNotYetValid: {Action: ActionReauth, Reason: ReasonAuthRequired, Message: "authentication required"},
}

// ResponseForOutcome maps an X-Auth-Outcome value to its client-facing failure.
// ok is false for an unrecognized or empty outcome.
func ResponseForOutcome(outcome string) (Failure, bool) {
	f, ok := outcomeResponses[outcome]
	return f, ok
}

// OutcomeForRevokeReason maps a stored blacklist reason to an auth outcome.
// Unknown reasons fail safe to OutcomeRevoked (force re-login) rather than
// keeping a possibly-dead session alive via refresh.
func OutcomeForRevokeReason(reason string) string {
	switch reason {
	case RevokeReasonUserUpdated, RevokeReasonTokenRefresh:
		return OutcomeStale
	case RevokeReasonLogout:
		return OutcomeLoggedOut
	case RevokeReasonTheft:
		return OutcomeRevoked
	case RevokeReasonAdminBan:
		return OutcomeSuspended
	default:
		return OutcomeRevoked
	}
}
