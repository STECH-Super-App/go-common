// Package auth provides authentication utilities.
package auth

import "github.com/golang-jwt/jwt/v5"

// TenantClaim is a single tenant-membership entry embedded in Claims.Tenants.
// Ownership is keyed on the bare tenant id — the pre-unification owner-type
// axis is gone. Compact JSON keys keep the signed token small:
//   - ID:   the tenant id (UUID)
//   - R:    the caller's role in that tenant. The gateway expands the role into
//     the long-form X-Tenant-Memberships header, so services never read this
//     claim directly; they consume the header via authz.ParseTenantMemberships.
//   - Kind: the tenant's kind, carried for client display only. Server-side
//     consumers never branch on it (spec §4); hence omitempty.
//   - At:   the membership's start, Unix seconds. Only Messenger reads it (the
//     gateway forwards it in the chat-only X-Tenant-Membership-Since header;
//     see authz.FormatMembershipSince) — it is never part of the
//     X-Tenant-Memberships header. Zero means unknown and is omitted from the
//     token; an older decoder ignores the key.
type TenantClaim struct {
	ID   string `json:"id"`
	R    string `json:"r"`
	Kind string `json:"kind,omitempty"`
	At   int64  `json:"at,omitempty"`
}

// Token kinds, the values of Claims.Kind (JWT claim "t"). One letter each: the
// claim rides in every access token, so its size is part of the token budget.
// The exact values are a wire contract between the signer (auth-service) and
// the enforcer (api-gateway).
const (
	// TokenKindAccess marks a full access token: it may carry user identity.
	TokenKindAccess = "a"
	// TokenKindRegistration marks a registration-scope token: it proves a
	// verified phone only and never carries a user identity.
	TokenKindRegistration = "r"
	// TokenKindConsent marks a consent-scope token (reserved for the
	// user-agreement acceptance flow).
	TokenKindConsent = "c"
)

// Claims represents the custom claims in a JWT token.
type Claims struct {
	UserID        string           `json:"sub"`
	Roles         []string         `json:"roles"`
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	Tenants       []TenantClaim    `json:"tns,omitempty"`
	ChatRead      *ChatReadContext `json:"chat_read,omitempty"`
	Avatar        string           `json:"avatar,omitempty"`
	Email         string           `json:"email,omitempty"`
	EmailVerified bool             `json:"email_verified,omitempty"`

	// Kind is the token's kind: one of the TokenKind* constants, or empty on a
	// token minted before the claim existed. api-gateway is the only enforcer:
	// it decides from Kind (and Scope) which identity headers a token may
	// produce; no other service reads it. It is NOT Type, which is the user's
	// type ("user", "service", ...) and says nothing about the token itself.
	Kind string `json:"t,omitempty"`

	// TenantsReadonly holds the caller's read-only memberships — seats in
	// SUSPENDED tenants. Tenants stays active-only (every non-chat consumer
	// authorizes writes from it); only Messenger reads this list, through the
	// chat-only X-Tenant-Readonly-Memberships header
	// (authz.FormatReadonlyMemberships). Tenants and TenantsReadonly together
	// hold at most the per-user membership cap.
	TenantsReadonly []TenantClaim `json:"tns_ro,omitempty"`

	Scope string `json:"scope,omitempty"` // "registration" for restricted tokens, empty for full tokens
	Phone string `json:"phone,omitempty"` // only set in registration-scope tokens
	jwt.RegisteredClaims
}
