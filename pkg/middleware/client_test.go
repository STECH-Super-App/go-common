package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/client"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
	"github.com/labstack/echo/v4"
)

func TestClientMiddleware_MissingDeviceID_Returns400(t *testing.T) {
	e := echo.New()
	e.Use(middleware.ClientMiddleware())
	e.GET("/", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserAgent, "STECH-Mobile-App")
	req.Header.Set(auth.HeaderRealIP, "127.0.0.1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestClientMiddleware_Valid_InjectsClientIntoContext(t *testing.T) {
	e := echo.New()
	var seen *client.Client
	e.Use(middleware.ClientMiddleware())
	e.GET("/", func(c echo.Context) error {
		if v := c.Get(string(auth.ContextKeyClient)); v != nil {
			seen = v.(*client.Client)
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderDeviceID, "11111111-1111-1111-1111-111111111111")
	req.Header.Set(auth.HeaderUserAgent, "STECH-Mobile-App")
	req.Header.Set(auth.HeaderRealIP, "127.0.0.1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if seen == nil {
		t.Fatal("expected *client.Client in context, got nil")
	}
	if seen.GetDeviceID() != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("unexpected device id: %s", seen.GetDeviceID())
	}
}
