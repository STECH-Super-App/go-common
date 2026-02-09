package middleware

import (
	"context"
	"net/http"

	"github.com/STECH-Super-App/go-common/pkg/auth"
)

// AuthMiddleware checks for the presence of the X-User-ID header.
// If present, it populates the context with user information.
// If missing, it returns 401 Unauthorized.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get(auth.HeaderUserID)
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, auth.ContextKeyUserID, userID)
		ctx = context.WithValue(ctx, auth.ContextKeyUserRole, r.Header.Get(auth.HeaderUserRole))
		ctx = context.WithValue(ctx, auth.ContextKeyUserName, r.Header.Get(auth.HeaderUserName))
		ctx = context.WithValue(ctx, auth.ContextKeyUserType, r.Header.Get(auth.HeaderUserType))
		ctx = context.WithValue(ctx, auth.ContextKeyTenantID, r.Header.Get(auth.HeaderTenantID))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuthMiddleware populates the context with user information if present.
// Unlike AuthMiddleware, it does not reject unauthenticated requests.
func OptionalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get(auth.HeaderUserID)
		if userID != "" {
			ctx := r.Context()
			ctx = context.WithValue(ctx, auth.ContextKeyUserID, userID)
			ctx = context.WithValue(ctx, auth.ContextKeyUserRole, r.Header.Get(auth.HeaderUserRole))
			ctx = context.WithValue(ctx, auth.ContextKeyUserName, r.Header.Get(auth.HeaderUserName))
			ctx = context.WithValue(ctx, auth.ContextKeyUserType, r.Header.Get(auth.HeaderUserType))
			ctx = context.WithValue(ctx, auth.ContextKeyTenantID, r.Header.Get(auth.HeaderTenantID))
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// UserIDFromContext retrieves the user ID from the context.
func UserIDFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(auth.ContextKeyUserID).(string)
	return val, ok
}

// UserRoleFromContext retrieves the user role from the context.
func UserRoleFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(auth.ContextKeyUserRole).(string)
	return val, ok
}

// UserNameFromContext retrieves the user name from the context.
func UserNameFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(auth.ContextKeyUserName).(string)
	return val, ok
}

// UserTypeFromContext retrieves the user type from the context.
func UserTypeFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(auth.ContextKeyUserType).(string)
	return val, ok
}

// TenantIDFromContext retrieves the tenant ID from the context.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(auth.ContextKeyTenantID).(string)
	return val, ok
}
