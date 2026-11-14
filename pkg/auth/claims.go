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

// Claims represents the custom claims in a JWT token.
//
// There is no chat_read claim any more (SL-25): it grew with a person's
// relationships and pushed the web cookie past 4,096 bytes (Critical Rule 20).
// A token minted before its removal may still carry one; encoding/json ignores
// the unknown key, so such a token keeps decoding (TestClaimsIgnoreRetiredChatReadClaim).
// Do not give the decoder DisallowUnknownFields.
type Claims struct {
	UserID        string        `json:"sub"`
	Roles         []string      `json:"roles"`
	Name          string        `json:"name"`
	Type          string        `json:"type"`
	Tenants       []TenantClaim `json:"tns,omitempty"`
	Avatar        string        `json:"avatar,omitempty"`
	Email         string        `json:"email,omitempty"`
	EmailVerified bool          `json:"email_verified,omitempty"`

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
