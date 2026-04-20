// Package auth provides authentication utilities.
package auth

import "github.com/golang-jwt/jwt/v5"

// TeamClaim is a single team-membership entry embedded in Claims.Teams.
// Short JSON keys keep the signed token compact:
//   - T: owner type, "U" (USER) or "T" (TENANT)
//   - O: owner id (UUID)
//   - R: role, "A" (ADMIN), "M" (MANAGER), or "O" (OPERATOR)
// The gateway expands these codes back to long-form strings in the
// X-Team-Memberships header so services never see the short form.
type TeamClaim struct {
	T string `json:"t"`
	O string `json:"o"`
	R string `json:"r"`
}

// Claims represents the custom claims in a JWT token.
type Claims struct {
	UserID        string      `json:"sub"`
	Roles         []string    `json:"roles"`
	Name          string      `json:"name"`
	Type          string      `json:"type"`
	Teams         []TeamClaim `json:"tms,omitempty"`
	Avatar        string      `json:"avatar,omitempty"`
	Email         string      `json:"email,omitempty"`
	EmailVerified bool        `json:"email_verified,omitempty"`

	Scope string `json:"scope,omitempty"` // "registration" for restricted tokens, empty for full tokens
	Phone string `json:"phone,omitempty"` // only set in registration-scope tokens
	jwt.RegisteredClaims
}
