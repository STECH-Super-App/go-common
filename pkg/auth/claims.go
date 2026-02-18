// Package auth provides authentication utilities.
package auth

import "github.com/golang-jwt/jwt/v5"

// Claims represents the custom claims in a JWT token.
type Claims struct {
	UserID   string   `json:"sub"`
	Roles    []string `json:"roles"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	TenantID string   `json:"tid"`
	jwt.RegisteredClaims
}
