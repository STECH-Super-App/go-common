package middleware

import (
	"github.com/STECH-Super-App/go-common/pkg/auth"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
)

// AuthMiddleware checks for the presence of the X-User-ID header.
// If present, it populates the context with user information.
// If missing, it returns 401 Unauthorized.
func AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Request().Header.Get(auth.HeaderUserID)
		if userID == "" {
			return response.JSONError(c, commonErrors.Unauthorized("Unauthorized", nil))
		}

		setContextValues(c)
		return next(c)
	}
}

// OptionalAuthMiddleware populates the context with user information if present.
// Unlike AuthMiddleware, it does not reject unauthenticated requests.
func OptionalAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Request().Header.Get(auth.HeaderUserID)
		if userID != "" {
			setContextValues(c)
		}
		return next(c)
	}
}

func setContextValues(c echo.Context) {
	c.Set(string(auth.ContextKeyUserID), c.Request().Header.Get(auth.HeaderUserID))
	c.Set(string(auth.ContextKeyUserRole), c.Request().Header.Get(auth.HeaderUserRole))
	c.Set(string(auth.ContextKeyUserName), c.Request().Header.Get(auth.HeaderUserName))
	c.Set(string(auth.ContextKeyUserType), c.Request().Header.Get(auth.HeaderUserType))
	c.Set(string(auth.ContextKeyTenantID), c.Request().Header.Get(auth.HeaderTenantID))
}

// UserIDFromContext retrieves the user ID from Echo context.
func UserIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserID)).(string)
	return val, ok
}

// UserRoleFromContext retrieves the user role from Echo context.
func UserRoleFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserRole)).(string)
	return val, ok
}

// UserNameFromContext retrieves the user name from Echo context.
func UserNameFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserName)).(string)
	return val, ok
}

// UserTypeFromContext retrieves the user type from Echo context.
func UserTypeFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserType)).(string)
	return val, ok
}

// TenantIDFromContext retrieves the tenant ID from Echo context.
func TenantIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyTenantID)).(string)
	return val, ok
}
