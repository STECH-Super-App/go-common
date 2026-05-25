// Package health provides a standardized health check handler for Echo-based services.
package health

import (
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
