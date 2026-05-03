package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

func newCtx(t *testing.T, paramName, paramValue string) echo.Context {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames(paramName)
	c.SetParamValues(paramValue)
	return c
}

func TestParseUUIDParamValid(t *testing.T) {
	c := newCtx(t, "id", "550e8400-e29b-41d4-a716-446655440000")
	got, err := middleware.ParseUUIDParam(c, "id", "TENANT_ID_INVALID")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got %q, want canonical UUID", got)
	}
}

func TestParseUUIDParamMissing(t *testing.T) {
	c := newCtx(t, "id", "")
	_, err := middleware.ParseUUIDParam(c, "id", "TENANT_ID_INVALID")
	if err == nil {
		t.Fatal("expected error for empty param, got nil")
	}
	var appErr *commonErrors.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error is not *AppError: %T", err)
	}
	if appErr.Code != http.StatusBadRequest {
		t.Errorf("code: want 400, got %d", appErr.Code)
	}
	if appErr.Reason != "TENANT_ID_INVALID" {
		t.Errorf("reason: want TENANT_ID_INVALID, got %q", appErr.Reason)
	}
	if appErr.Message != "invalid id" {
		t.Errorf("message: want 'invalid id', got %q", appErr.Message)
	}
}

func TestParseUUIDParamMalformed(t *testing.T) {
	c := newCtx(t, "transferId", "not-a-uuid")
	_, err := middleware.ParseUUIDParam(c, "transferId", "TENANT_TRANSFER_ID_INVALID")
	if err == nil {
		t.Fatal("expected error for malformed UUID, got nil")
	}
	var appErr *commonErrors.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error is not *AppError: %T", err)
	}
	if appErr.Code != http.StatusBadRequest {
		t.Errorf("code: want 400, got %d", appErr.Code)
	}
	if appErr.Reason != "TENANT_TRANSFER_ID_INVALID" {
		t.Errorf("reason: want TENANT_TRANSFER_ID_INVALID, got %q", appErr.Reason)
	}
	if appErr.Message != "invalid transferId" {
		t.Errorf("message: want 'invalid transferId', got %q", appErr.Message)
	}
	if appErr.Err == nil {
		t.Error("Err: want underlying parse error chained, got nil")
	}
}
