package middleware

import (
	"net/http"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/client"
	"github.com/STECH-Super-App/go-common/pkg/clientip"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
)

// ClientMiddleware extracts client information from request headers and
// rejects requests that are missing or have invalid client info.
//
// All auth-sensitive routes MUST sit behind this middleware so downstream
// code can rely on a non-nil *client.Client in the echo context.
//
// The client's address is the gateway's X-Client-IP (go-common/pkg/clientip)
// and nothing else. It used to be X-Real-IP, then X-Forwarded-For, then the TCP
// peer — all three are either typed by the caller or, behind ingress, the
// ingress pod (GW-04, ID-24). A request without a valid X-Client-IP is refused
// 400 COMMON_CLIENT_INFO_INVALID: roll api-gateway (which writes the header)
// out before a service takes this version.
func ClientMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			deviceID := c.Request().Header.Get(auth.HeaderDeviceID)
			userAgent := c.Request().Header.Get(auth.HeaderUserAgent)

			// An absent address stays "": client.NewClient refuses it as ErrInvalidIP.
			var ip string
			if addr, ok := clientip.FromEcho(c); ok {
				ip = addr.String()
			}

			cl, err := client.NewClient(deviceID, userAgent, ip)
			if err != nil {
				return response.JSONError(c, commonErrors.New(http.StatusBadRequest).
					Reason("COMMON_CLIENT_INFO_INVALID").
					Message("missing or invalid client info: "+err.Error()).
					Cause(err).
					Build())
			}

			c.Set(string(auth.ContextKeyClient), cl)
			return next(c)
		}
	}
}
