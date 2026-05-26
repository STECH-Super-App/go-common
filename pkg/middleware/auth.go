package middleware

import (
	"log"
	"net/http"
	"strings"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
)

// AuthMiddleware enforces authentication for protected routes. It reads the
// gateway-set X-Auth-Outcome header: a recognized outcome yields a 401 carrying
// the X-Auth-Action the client must take (refresh / reauth) and the matching
// COMMON_* reason. With no failing outcome, a present X-User-ID means
// authenticated; an absent one means no credential (COMMON_AUTH_REQUIRED).
func AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if outcome := c.Request().Header.Get(auth.HeaderAuthOutcome); outcome != "" {
			failure, ok := auth.ResponseForOutcome(outcome)
			if !ok {
				// Unrecognized outcome → fail safe to reauth rather than fall through.
				failure = auth.Failure{Action: auth.ActionReauth, Reason: auth.ReasonAuthRequired, Message: "authentication required"}
			}
			c.Response().Header().Set(auth.HeaderAuthAction, failure.Action)
			return response.JSONError(c, commonErrors.New(http.StatusUnauthorized).
				Reason(failure.Reason).
				Message(failure.Message).
				Build())
		}

		userID := c.Request().Header.Get(auth.HeaderUserID)
		if userID == "" {
			c.Response().Header().Set(auth.HeaderAuthAction, auth.ActionReauth)
			return response.JSONError(c, commonErrors.New(http.StatusUnauthorized).
				Reason(auth.ReasonAuthRequired).
				Message("authentication required").
				Build())
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

// RegistrationAuthMiddleware checks for the presence of the X-Phone and X-Scope headers.
// It is used for registration-scope tokens that don't have a user ID yet.
func RegistrationAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		phone := c.Request().Header.Get(auth.HeaderPhone)
		scope := c.Request().Header.Get(auth.HeaderScope)

		log.Printf("RegistrationAuthMiddleware - Phone: %q, Scope: %q", phone, scope)

		if phone == "" || scope != "registration" {
			return response.JSONError(c, commonErrors.New(http.StatusUnauthorized).
				Reason("COMMON_REGISTRATION_TOKEN_REQUIRED").
				Message("valid registration token required").
				Build())
		}

		setContextValues(c)
		return next(c)
	}
}

func setContextValues(c echo.Context) {
	c.Set(string(auth.ContextKeyUserID), c.Request().Header.Get(auth.HeaderUserID))
	c.Set(string(auth.ContextKeyUserRoles), c.Request().Header.Get(auth.HeaderUserRoles))
	c.Set(string(auth.ContextKeyUserName), c.Request().Header.Get(auth.HeaderUserName))
	c.Set(string(auth.ContextKeyUserType), c.Request().Header.Get(auth.HeaderUserType))
	c.Set(string(auth.ContextKeyTeamMemberships), authz.Parse(c.Request().Header.Get(auth.HeaderTeamMemberships)))

	c.Set(string(auth.ContextKeyScope), c.Request().Header.Get(auth.HeaderScope))
	c.Set(string(auth.ContextKeyPhone), c.Request().Header.Get(auth.HeaderPhone))
}

// UserIDFromContext retrieves the user ID from Echo context.
func UserIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserID)).(string)
	return val, ok
}

// UserRolesFromContext retrieves the user roles from Echo context.
func UserRolesFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserRoles)).(string)
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

// ScopeFromContext retrieves the token scope from Echo context.
func ScopeFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyScope)).(string)
	return val, ok
}

// PhoneFromContext retrieves the phone number from Echo context.
func PhoneFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyPhone)).(string)
	return val, ok
}

// AdminMiddleware rejects requests that do not carry the "admin" role.
// Must be applied after AuthMiddleware so that context values are populated.
func AdminMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		rolesStr, ok := UserRolesFromContext(c)
		if !ok {
			return response.JSONError(c, commonErrors.New(http.StatusForbidden).
				Reason("COMMON_ADMIN_REQUIRED").
				Message("admin role required").
				Build())
		}

		for _, role := range strings.Split(rolesStr, ",") {
			if strings.TrimSpace(role) == "admin" {
				return next(c)
			}
		}

		return response.JSONError(c, commonErrors.New(http.StatusForbidden).
			Reason("COMMON_ADMIN_REQUIRED").
			Message("admin role required").
			Build())
	}
}
