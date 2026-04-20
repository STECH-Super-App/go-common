package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
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
	assert.Equal(t, "Unauthorized", errObj["message"])
}

func TestAuthMiddleware_RevokedToken(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Token-Revoked", "true")
	req.Header.Set(auth.HeaderUserID, "user-123") // Even with user ID, revoked takes priority
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := AuthMiddleware(func(_ echo.Context) error {
		t.Fatal("handler should not be called for revoked token")
		return nil
	})

	err := handler(c)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	errObj := body["error"].(map[string]interface{})
	assert.Contains(t, errObj["message"], "revoked")
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
	// Gateway sets X-Token-Revoked but does NOT set X-User-ID for revoked tokens.
	req.Header.Set("X-Token-Revoked", "true")
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
