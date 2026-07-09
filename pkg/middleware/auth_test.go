package middleware

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware_ValidUser(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "user-123")
	req.Header.Set(auth.HeaderUserRoles, "admin")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	called := false
	handler := AuthMiddleware(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "OK")
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user-123", c.Get(string(auth.ContextKeyUserID)))
}

func TestAuthMiddleware_MissingUserID(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := AuthMiddleware(func(_ echo.Context) error {
		t.Fatal("handler should not be called")
		return nil
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	errObj := body["error"].(map[string]interface{})
	assert.Equal(t, "authentication required", errObj["message"])
	assert.Equal(t, "COMMON_AUTH_REQUIRED", errObj["reason"])
	assert.Equal(t, auth.ActionReauth, rec.Header().Get(auth.HeaderAuthAction))
}

func TestAuthMiddleware_OutcomeMapsToActionAndReason(t *testing.T) {
	cases := []struct {
		outcome string
		action  string
		reason  string
	}{
		{auth.OutcomeExpired, auth.ActionRefresh, "COMMON_TOKEN_EXPIRED"},
		{auth.OutcomeStale, auth.ActionRefresh, "COMMON_TOKEN_STALE"},
		{auth.OutcomeLoggedOut, auth.ActionReauth, "COMMON_SESSION_LOGGED_OUT"},
		{auth.OutcomeRevoked, auth.ActionReauth, "COMMON_SESSION_REVOKED"},
		{auth.OutcomeSuspended, auth.ActionReauth, "COMMON_ACCOUNT_SUSPENDED"},
		{auth.OutcomeUntrusted, auth.ActionReauth, "COMMON_AUTH_REQUIRED"},
		{auth.OutcomeNotYetValid, auth.ActionReauth, "COMMON_AUTH_REQUIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.outcome, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(auth.HeaderAuthOutcome, tc.outcome)
			req.Header.Set(auth.HeaderUserID, "user-123") // outcome takes priority over identity
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := AuthMiddleware(func(_ echo.Context) error {
				t.Fatal("handler must not be called for a failed outcome")
				return nil
			})

			require.NoError(t, handler(c))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, tc.action, rec.Header().Get(auth.HeaderAuthAction))

			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			errObj := body["error"].(map[string]interface{})
			assert.Equal(t, tc.reason, errObj["reason"])
		})
	}
}

func TestOptionalAuthMiddleware_WithUser(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "user-456")
	req.Header.Set(auth.HeaderUserRoles, "user")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	called := false
	handler := OptionalAuthMiddleware(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "OK")
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "user-456", c.Get(string(auth.ContextKeyUserID)))
}

func TestOptionalAuthMiddleware_NoUser(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	called := false
	handler := OptionalAuthMiddleware(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "OK")
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.True(t, called, "handler should be called even without auth")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, c.Get(string(auth.ContextKeyUserID)))
}

func TestOptionalAuthMiddleware_RevokedToken(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Gateway sets X-Auth-Outcome but does NOT set X-User-ID for bad tokens.
	req.Header.Set(auth.HeaderAuthOutcome, auth.OutcomeLoggedOut)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	called := false
	handler := OptionalAuthMiddleware(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "OK")
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.True(t, called, "optional auth should pass through revoked tokens")
	assert.Equal(t, http.StatusOK, rec.Code)
	// User context should NOT be populated — treated as anonymous.
	assert.Nil(t, c.Get(string(auth.ContextKeyUserID)))
}

func TestAuthMiddleware_ParsesTeamMemberships(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "user-xyz")
	req.Header.Set(auth.HeaderTeamMemberships, "TENANT:tn-1=MANAGER,USER:u-1=ADMIN")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := AuthMiddleware(func(c echo.Context) error {
		mems, ok := authz.MembershipsFromContext(c)
		assert.True(t, ok)
		assert.Equal(t, authz.Memberships{
			{OwnerType: authz.OwnerTenant, OwnerID: "tn-1", Role: authz.RoleManager},
			{OwnerType: authz.OwnerUser, OwnerID: "u-1", Role: authz.RoleAdmin},
		}, mems)
		return c.String(http.StatusOK, "OK")
	})

	require.NoError(t, handler(c))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireUserUUID(t *testing.T) {
	validID := "550e8400-e29b-41d4-a716-446655440000"

	cases := []struct {
		name     string
		setID    bool // whether to set the X-User-ID header at all
		rawID    string
		wantUUID uuid.UUID
		wantErr  bool
	}{
		{
			name:     "valid uuid",
			setID:    true,
			rawID:    validID,
			wantUUID: uuid.MustParse(validID),
			wantErr:  false,
		},
		{
			name:     "missing id (no AuthMiddleware / no header)",
			setID:    false,
			wantUUID: uuid.Nil,
			wantErr:  true,
		},
		{
			name:     "empty id",
			setID:    true,
			rawID:    "",
			wantUUID: uuid.Nil,
			wantErr:  true,
		},
		{
			name:     "malformed uuid",
			setID:    true,
			rawID:    "not-a-uuid",
			wantUUID: uuid.Nil,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			if tc.setID {
				// Populate the context key the same way AuthMiddleware does,
				// via setContextValues reading the X-User-ID header.
				req.Header.Set(auth.HeaderUserID, tc.rawID)
				setContextValues(c)
			}

			gotUUID, gotErr := RequireUserUUID(c)
			assert.Equal(t, tc.wantUUID, gotUUID)
			if tc.wantErr {
				require.NotNil(t, gotErr)
				// The typed 401 carries the canonical reason so every caller
				// surfaces the same i18n key.
				assert.Equal(t, http.StatusUnauthorized, gotErr.Code)
				assert.Equal(t, auth.ReasonAuthRequired, gotErr.Reason)
			} else {
				assert.Nil(t, gotErr)
			}
		})
	}
}

func TestRegistrationAuthMiddleware_ValidRegistrationToken(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(auth.HeaderPhone, "+77001234567")
	req.Header.Set(auth.HeaderScope, "registration")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	called := false
	handler := RegistrationAuthMiddleware(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "OK")
	})

	require.NoError(t, handler(c))
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "+77001234567", c.Get(string(auth.ContextKeyPhone)))
	assert.Equal(t, "registration", c.Get(string(auth.ContextKeyScope)))
}

func TestRegistrationAuthMiddleware_Rejects(t *testing.T) {
	cases := []struct {
		name  string
		phone string
		scope string
	}{
		{name: "missing phone", phone: "", scope: "registration"},
		{name: "wrong scope", phone: "+77001234567", scope: "access"},
		{name: "missing both", phone: "", scope: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.phone != "" {
				req.Header.Set(auth.HeaderPhone, tc.phone)
			}
			if tc.scope != "" {
				req.Header.Set(auth.HeaderScope, tc.scope)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegistrationAuthMiddleware(func(_ echo.Context) error {
				t.Fatal("handler must not be called")
				return nil
			})

			require.NoError(t, handler(c))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)

			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			errObj := body["error"].(map[string]interface{})
			assert.Equal(t, "COMMON_REGISTRATION_TOKEN_REQUIRED", errObj["reason"])
		})
	}
}

// TestRegistrationAuthMiddleware_NeverLogsPhone is the PII regression test: the
// middleware once contained a debug log.Printf that wrote the caller's full
// phone number to the process log. Capture the standard logger's output across
// both the pass and reject paths and assert the phone never appears.
func TestRegistrationAuthMiddleware_NeverLogsPhone(t *testing.T) {
	const phone = "+77001234567"

	var captured bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(prev)

	for _, scope := range []string{"registration", "access"} {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(auth.HeaderPhone, phone)
		req.Header.Set(auth.HeaderScope, scope)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		handler := RegistrationAuthMiddleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "OK")
		})
		require.NoError(t, handler(c))
	}

	assert.NotContains(t, captured.String(), phone,
		"RegistrationAuthMiddleware must never log the caller's phone number")
}

func TestAuthMiddleware_NoTeamMembershipsHeaderYieldsNil(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "user-xyz")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := AuthMiddleware(func(c echo.Context) error {
		mems, ok := authz.MembershipsFromContext(c)
		// Nil slice stored; ok=true because the key exists, but the value is nil.
		assert.True(t, ok || mems == nil)
		assert.Nil(t, mems)
		return c.String(http.StatusOK, "OK")
	})

	require.NoError(t, handler(c))
	assert.Equal(t, http.StatusOK, rec.Code)
}
