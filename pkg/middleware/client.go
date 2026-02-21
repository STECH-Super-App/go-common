package middleware

import (
	"net"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/client"
	"github.com/labstack/echo/v4"
)

// ClientMiddleware extracts client information from request headers.
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

			// Create Client VO
			cl, err := client.NewClient(deviceID, userAgent, ip)
			if err != nil {
				c.Logger().Error(err)
			}

			if cl != nil {
				c.Set(string(auth.ContextKeyClient), cl)
			}

			return next(c)
		}
	}
}
