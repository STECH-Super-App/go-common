package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/labstack/echo/v4"
)

func runGate(t *testing.T, roles string, min authz.GlobalRole) int {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "11111111-1111-1111-1111-111111111111")
	req.Header.Set(auth.HeaderUserRoles, roles)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h := AuthMiddleware(RequireGlobalRole(min)(func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	}))
	_ = h(c)
	return rec.Code
}

func TestRequireGlobalRole_Hierarchy(t *testing.T) {
	if code := runGate(t, "user,admin", authz.GlobalRoleSupportAgent); code != http.StatusOK {
		t.Fatalf("admin should inherit support_agent gate, got %d", code)
	}
	if code := runGate(t, "user,support_agent", authz.GlobalRoleSupportAgent); code != http.StatusOK {
		t.Fatalf("support_agent should pass support_agent gate, got %d", code)
	}
	if code := runGate(t, "user", authz.GlobalRoleSupportAgent); code != http.StatusForbidden {
		t.Fatalf("plain user should be 403 on support_agent gate, got %d", code)
	}
	if code := runGate(t, "user,support_agent", authz.GlobalRoleAdmin); code != http.StatusForbidden {
		t.Fatalf("support_agent must NOT pass admin gate, got %d", code)
	}
}

func TestAdminMiddleware_StillAdminOnly(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserID, "11111111-1111-1111-1111-111111111111")
	req.Header.Set(auth.HeaderUserRoles, "user,support_agent")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h := AuthMiddleware(AdminMiddleware(func(c echo.Context) error { return c.NoContent(http.StatusOK) }))
	_ = h(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("support_agent must not pass AdminMiddleware, got %d", rec.Code)
	}
}
