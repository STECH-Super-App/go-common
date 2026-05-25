package health

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestRegister_GET verifies GET /health returns 200 with the success envelope.
func TestRegister_GET(t *testing.T) {
	e := echo.New()
	Register(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: got status %d, want %d", rec.Code, http.StatusOK)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("GET /health: body %q missing status:ok", body)
	}
}

// TestRegister_HEAD verifies HEAD /health returns 200. HEAD is what
// `wget --spider` (our docker-compose healthcheck) issues; without an explicit
// HEAD route Echo answers 405 and the container is silently marked unhealthy.
func TestRegister_HEAD(t *testing.T) {
	e := echo.New()
	Register(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /health: got status %d, want %d", rec.Code, http.StatusOK)
	}
}
