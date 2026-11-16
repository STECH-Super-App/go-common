package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/response"
)

func serve(e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// SL-30: health.Register serves a constant 200, so a pod that is shutting down
// kept receiving traffic from a load balancer that polls it. /readyz answers
// 200 until the lifecycle App's Stopping() channel closes, and 503 after.
func TestRegisterReadiness_FlipsWhenStoppingCloses(t *testing.T) {
	stopping := make(chan struct{})
	e := echo.New()
	RegisterReadiness(e, stopping)

	assert.Equal(t, http.StatusOK, serve(e, http.MethodGet, "/readyz").Code)
	assert.Equal(t, http.StatusOK, serve(e, http.MethodHead, "/readyz").Code, "HEAD for wget --spider probes")

	close(stopping)

	rec := serve(e, http.MethodGet, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var res response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.NotNil(t, res.Error)
	assert.Equal(t, ReasonShuttingDown, res.Error.Reason)
	assert.Equal(t, http.StatusServiceUnavailable, serve(e, http.MethodHead, "/readyz").Code)
}

func TestRegisterReadiness_NilStoppingIsAlwaysReady(t *testing.T) {
	e := echo.New()
	RegisterReadiness(e, nil)

	assert.Equal(t, http.StatusOK, serve(e, http.MethodGet, "/readyz").Code)
}

func TestHealthStaysUpWhileStopping(t *testing.T) {
	// Liveness must not flip: a failing /health makes the kubelet restart a pod
	// that is merely draining.
	stopping := make(chan struct{})
	close(stopping)
	e := echo.New()
	Register(e)
	RegisterReadiness(e, stopping)

	assert.Equal(t, http.StatusOK, serve(e, http.MethodGet, "/health").Code)
	assert.Equal(t, http.StatusServiceUnavailable, serve(e, http.MethodGet, "/readyz").Code)
}
