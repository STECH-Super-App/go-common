package middleware

import (
	"net"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/client"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
)

// ClientMiddleware extracts client information from request headers and
// rejects requests that are missing or have invalid client info.
//
// All auth-sensitive routes MUST sit behind this middleware so downstream
// code can rely on a non-nil *client.Client in the echo context.
func ClientMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			deviceID := c.Request().Header.Get(auth.HeaderDeviceID)
			userAgent := c.Request().Header.Get(auth.HeaderUserAgent)
			ip := c.Request().Header.Get(auth.HeaderRealIP)

			if ip == "" {
				ip = c.Request().Header.Get(auth.HeaderForwardedFor)
			}
			if ip == "" {
				ip, _, _ = net.SplitHostPort(c.Request().RemoteAddr)
			}

			cl, err := client.NewClient(deviceID, userAgent, ip)
			if err != nil {
				return response.JSONError(c, commonErrors.BadRequestWithReason(
					"missing or invalid client info: "+err.Error(),
					"CLIENT_INFO_INVALID",
					err,
				))
			}

			c.Set(string(auth.ContextKeyClient), cl)
			return next(c)
		}
	}
}
