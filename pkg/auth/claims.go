// Package auth provides authentication utilities.
package auth

import "github.com/golang-jwt/jwt/v5"

// Claims represents the custom claims in a JWT token.
type Claims struct {
	UserID  string   `json:"sub"`
	Roles   []string `json:"roles"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Tenants []string `json:"tenants,omitempty"`
	Avatar  string   `json:"avatar,omitempty"`
	Email   string   `json:"email,omitempty"`

	Scope string `json:"scope,omitempty"` // "registration" for restricted tokens, empty for full tokens
	Phone string `json:"phone,omitempty"` // only set in registration-scope tokens
	jwt.RegisteredClaims
}
