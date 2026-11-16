// Package health provides a standardized health check handler for Echo-based services.
package health

import (
	"net/http"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
)

// status holds the data payload for the health check response.
type status struct {
	Status string `json:"status"`
}

// Register adds GET and HEAD /health endpoints to the given Echo instance.
// Returns 200 OK with {"success": true, "data": {"status": "ok"}}.
//
// HEAD is registered alongside GET so lightweight liveness probes that issue a
// HEAD request — e.g. `wget --spider`, the default in our docker-compose
// healthchecks — succeed instead of getting a 405. Echo otherwise rejects HEAD
// on a GET-only route, which silently leaves the container marked unhealthy.
func Register(e *echo.Echo) {
	e.GET("/health", handler)
	e.HEAD("/health", handler)
}

func handler(c echo.Context) error {
	return response.Success(c, &status{Status: "ok"})
}

// ReasonShuttingDown is the Reason of the 503 /readyz answers once shutdown has
// begun.
const ReasonShuttingDown = "COMMON_SHUTTING_DOWN"

// RegisterReadiness adds GET and HEAD /readyz: 200 while the process is
// serving, 503 (Reason COMMON_SHUTTING_DOWN) once stopping is closed. Pass
// lifecycle.App.Stopping(), which closes when shutdown begins — before the drain
// delay and before any server stops — so a readiness probe pulls the pod out of
// rotation while it is still draining.
//
// Keep /health (Register) as the liveness probe: it must stay 200 while the pod
// drains, or the kubelet restarts a pod that is only finishing its work. A nil
// stopping channel is always ready.
func RegisterReadiness(e *echo.Echo, stopping <-chan struct{}) {
	h := func(c echo.Context) error {
		select {
		case <-stopping: // a nil channel blocks forever: never ready-flipped
			return response.JSONError(c, commonErrors.New(http.StatusServiceUnavailable).
				Reason(ReasonShuttingDown).
				Message("service is shutting down").
				Build())
		default:
			return response.Success(c, &status{Status: "ok"})
		}
	}
	e.GET("/readyz", h)
	e.HEAD("/readyz", h)
}
