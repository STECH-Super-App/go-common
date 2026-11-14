package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/client"
	"github.com/STECH-Super-App/go-common/pkg/clientip"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
	"github.com/labstack/echo/v4"
)

func TestClientMiddleware_MissingDeviceID_Returns400(t *testing.T) {
	e := echo.New()
	e.Use(middleware.ClientMiddleware())
	e.GET("/", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(auth.HeaderUserAgent, "STECH-Mobile-App")
	req.Header.Set(clientip.Header, "127.0.0.1")
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
	req.Header.Set(clientip.Header, "127.0.0.1")
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

// The address a ClientMiddleware client records is the gateway's X-Client-IP and
// nothing else (spec §3.3): not a forwarding header the caller typed, not the
// TCP peer. Without it the request is refused rather than recorded under a
// guess (the old code fell back to the peer, i.e. the ingress pod).
func TestClientMiddleware_ClientAddressIsXClientIPOnly(t *testing.T) {
	e := echo.New()
	var seen *client.Client
	e.Use(middleware.ClientMiddleware())
	e.GET("/", func(c echo.Context) error {
		seen = c.Get(string(auth.ContextKeyClient)).(*client.Client)
		return c.NoContent(http.StatusOK)
	})
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.9.9.9:4000"
		req.Header.Set(auth.HeaderDeviceID, "11111111-1111-1111-1111-111111111111")
		req.Header.Set(auth.HeaderUserAgent, "STECH-Mobile-App")
		return req
	}

	req := newReq()
	req.Header.Set("X-Real-IP", "203.0.113.1")
	req.Header.Set("X-Forwarded-For", "203.0.113.2")
	req.Header.Set(clientip.Header, "203.0.113.3")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen.GetIP() != "203.0.113.3" {
		t.Fatalf("recorded IP %q, want the X-Client-IP value", seen.GetIP())
	}

	// Forwarding headers and a peer are not an address.
	req = newReq()
	req.Header.Set("X-Real-IP", "203.0.113.1")
	req.Header.Set("X-Forwarded-For", "203.0.113.2")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without X-Client-IP, got %d", rec.Code)
	}
}
