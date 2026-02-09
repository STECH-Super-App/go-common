package middleware

import (
	"net/http"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

// EchoAuthMiddleware checks for the presence of the X-User-ID header.
// If present, it populates the context with user information.
// If missing, it returns 401 Unauthorized.
func EchoAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Request().Header.Get(auth.HeaderUserID)
		if userID == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}

		setEchoContextValues(c)
		return next(c)
	}
}

// EchoOptionalAuthMiddleware populates the context with user information if present.
// Unlike EchoAuthMiddleware, it does not reject unauthenticated requests.
func EchoOptionalAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Request().Header.Get(auth.HeaderUserID)
		if userID != "" {
			setEchoContextValues(c)
		}
		return next(c)
	}
}

func setEchoContextValues(c echo.Context) {
	c.Set(string(auth.ContextKeyUserID), c.Request().Header.Get(auth.HeaderUserID))
	c.Set(string(auth.ContextKeyUserRole), c.Request().Header.Get(auth.HeaderUserRole))
	c.Set(string(auth.ContextKeyUserName), c.Request().Header.Get(auth.HeaderUserName))
	c.Set(string(auth.ContextKeyUserType), c.Request().Header.Get(auth.HeaderUserType))
	c.Set(string(auth.ContextKeyTenantID), c.Request().Header.Get(auth.HeaderTenantID))
}

// EchoUserIDFromContext retrieves the user ID from Echo context.
func EchoUserIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserID)).(string)
	return val, ok
}

// EchoUserRoleFromContext retrieves the user role from Echo context.
func EchoUserRoleFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserRole)).(string)
	return val, ok
}

// EchoUserNameFromContext retrieves the user name from Echo context.
func EchoUserNameFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserName)).(string)
	return val, ok
}

// EchoUserTypeFromContext retrieves the user type from Echo context.
func EchoUserTypeFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserType)).(string)
	return val, ok
}

// EchoTenantIDFromContext retrieves the tenant ID from Echo context.
func EchoTenantIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyTenantID)).(string)
	return val, ok
}
