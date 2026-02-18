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

// Register adds a GET /health endpoint to the given Echo instance.
// Returns 200 OK with {"success": true, "data": {"status": "ok"}}.
func Register(e *echo.Echo) {
	e.GET("/health", handler)
}

func handler(c echo.Context) error {
	return response.Success(c, &status{Status: "ok"})
}
