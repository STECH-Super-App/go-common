package middleware

import (
	"log"
	"net/http"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/google/uuid"
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
	c.Set(string(auth.ContextKeyTenantMemberships), authz.ParseTenantMemberships(c.Request().Header.Get(authz.HeaderTenantMemberships)))

	c.Set(string(auth.ContextKeyScope), c.Request().Header.Get(auth.HeaderScope))
	c.Set(string(auth.ContextKeyPhone), c.Request().Header.Get(auth.HeaderPhone))
}

// UserIDFromContext retrieves the user ID from Echo context.
func UserIDFromContext(c echo.Context) (string, bool) {
	val, ok := c.Get(string(auth.ContextKeyUserID)).(string)
	return val, ok
}

// RequireUserUUID returns the authenticated caller's user-id parsed as a
// uuid.UUID, or a ready-to-return typed 401 AppError when no valid id is
// present. It builds on UserIDFromContext (which AuthMiddleware populates from
// the X-User-ID header), so AuthMiddleware must run on the route first.
//
// Returning the typed error — rather than a bool — is the point: services that
// model the caller as a uuid.UUID otherwise each re-implement the identical
// "read context id, parse it, build a 401 on failure" sequence (previously
// duplicated as inbox-service's recipientFromHeader and chat-service's
// callerUUIDFromContext, which had even drifted to different Reason codes). The
// returned AppError carries the canonical COMMON_AUTH_REQUIRED reason so every
// caller collapses to:
//
//	id, appErr := middleware.RequireUserUUID(c)
//	if appErr != nil {
//	    return response.JSONError(c, appErr)
//	}
//
// A non-nil error is returned when the id is missing from the context (the
// route lacked AuthMiddleware, or the gateway sent no identity) or is present
// but not a valid UUID; the two cases differ only in the English message.
func RequireUserUUID(c echo.Context) (uuid.UUID, *commonErrors.AppError) {
	raw, ok := UserIDFromContext(c)
	if !ok || raw == "" {
		return uuid.Nil, commonErrors.New(http.StatusUnauthorized).
			Reason(auth.ReasonAuthRequired).
			Message("user id missing from request context").
			Build()
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, commonErrors.New(http.StatusUnauthorized).
			Reason(auth.ReasonAuthRequired).
			Message("user id is not a valid UUID").
			Cause(err).
			Build()
	}
	return id, nil
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

// globalRoleDenial maps a required global role to the typed 403 it raises when
// the caller falls short. Kept beside RequireGlobalRole so the reason codes —
// canonical i18n keys the frontend renders — live in one place.
var globalRoleDenial = map[authz.GlobalRole]struct {
	Reason  string
	Message string
}{
	authz.GlobalRoleAdmin:        {"COMMON_ADMIN_REQUIRED", "admin role required"},
	authz.GlobalRoleSupportAgent: {"COMMON_SUPPORT_AGENT_REQUIRED", "support agent role required"},
}

// RequireGlobalRole returns middleware that admits callers whose global roles
// meet minRole in the hierarchy admin > support_agent > user — a higher role
// satisfies a lower requirement (admin satisfies a support_agent gate). It must
// run after AuthMiddleware so the roles are in context. On failure it returns
// the typed 403 registered for minRole (default COMMON_ADMIN_REQUIRED, fail-closed).
func RequireGlobalRole(minRole authz.GlobalRole) echo.MiddlewareFunc {
	denial, ok := globalRoleDenial[minRole]
	if !ok {
		denial = globalRoleDenial[authz.GlobalRoleAdmin]
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rolesStr, _ := UserRolesFromContext(c)
			if authz.HasMinGlobalRole(rolesStr, minRole) {
				return next(c)
			}
			return response.JSONError(c, commonErrors.New(http.StatusForbidden).
				Reason(denial.Reason).
				Message(denial.Message).
				Build())
		}
	}
}

// AdminMiddleware admits only callers carrying the global admin role. Retained
// as the canonical name mounted across services; it now delegates to
// RequireGlobalRole(admin), preserving the COMMON_ADMIN_REQUIRED reason. Must be
// applied after AuthMiddleware.
func AdminMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return RequireGlobalRole(authz.GlobalRoleAdmin)(next)
}
