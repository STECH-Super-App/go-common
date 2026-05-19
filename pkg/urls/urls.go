// Package urls constructs environment-aware URLs for STECH services.
// It replaces the standalone url-service with an in-process package.
// All public URLs route through Env.AppBaseURL; deep-links use Env.DeepLinkScheme.
package urls

import (
	"net/url"
	"strings"
)

// Env holds the runtime URL configuration for a deployment environment.
type Env struct {
	// AppBaseURL is the public-facing base URL of the API gateway (e.g. "https://app.stech.kz").
	AppBaseURL string
	// DeepLinkScheme is the mobile deep-link URI scheme (e.g. "superapp").
	DeepLinkScheme string
}

// OAuthCallbackVK returns the VK OAuth2 redirect URI.
func OAuthCallbackVK(env Env) string {
	return env.AppBaseURL + "/auth/oauth2/vk/callback"
}

// OAuthCallbackYandex returns the Yandex OAuth2 redirect URI.
func OAuthCallbackYandex(env Env) string {
	return env.AppBaseURL + "/auth/oauth2/yandex/callback"
}

// MediaFile returns the public URL for a stored media file identified by key.
// Each path segment of key is percent-encoded individually so embedded slashes survive.
func MediaFile(env Env, key string) string {
	segments := strings.Split(key, "/")
	escaped := make([]string, len(segments))
	for i, seg := range segments {
		escaped[i] = url.PathEscape(seg)
	}
	return env.AppBaseURL + "/media/files/" + strings.Join(escaped, "/")
}

// PhoneVerification returns the URL a user visits to confirm their phone number.
func PhoneVerification(env Env, code string) string {
	return env.AppBaseURL + "/verify?code=" + url.QueryEscape(code) + "&type=phone"
}

// EmailVerification returns the URL a user visits to confirm their email address.
func EmailVerification(env Env, code string) string {
	return env.AppBaseURL + "/verify?code=" + url.QueryEscape(code) + "&type=email"
}

// UserProfile returns the deep-link URI for a user profile page.
func UserProfile(env Env, userID string) string {
	return env.DeepLinkScheme + "://profile/" + userID
}

// TenantDashboard returns the deep-link URI for a tenant dashboard.
func TenantDashboard(env Env, tenantID string) string {
	return env.DeepLinkScheme + "://tenant/" + tenantID + "/dashboard"
}

// TenantShare returns the public shareable URL for a tenant profile.
func TenantShare(env Env, tenantID string) string {
	return env.AppBaseURL + "/tenants/" + tenantID
}

// PasswordReset returns the URL a user visits to reset their password.
func PasswordReset(env Env, token string) string {
	return env.AppBaseURL + "/reset-password?token=" + url.QueryEscape(token)
}
