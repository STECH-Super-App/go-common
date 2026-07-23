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
type TenantClaim struct {
	ID   string `json:"id"`
	R    string `json:"r"`
	Kind string `json:"kind,omitempty"`
}

// Claims represents the custom claims in a JWT token.
type Claims struct {
	UserID        string        `json:"sub"`
	Roles         []string      `json:"roles"`
	Name          string        `json:"name"`
	Type          string        `json:"type"`
	Tenants       []TenantClaim `json:"tns,omitempty"`
	Avatar        string        `json:"avatar,omitempty"`
	Email         string        `json:"email,omitempty"`
	EmailVerified bool          `json:"email_verified,omitempty"`

	Scope string `json:"scope,omitempty"` // "registration" for restricted tokens, empty for full tokens
	Phone string `json:"phone,omitempty"` // only set in registration-scope tokens
	jwt.RegisteredClaims
}
