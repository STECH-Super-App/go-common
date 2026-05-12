// Device-identification helpers.
//
// X-Device-Id is the client-supplied device identifier. The api-gateway
// validates format but does not generate or overwrite it. Empty is a valid
// sentinel meaning "no client device".
//
// X-Device-Fingerprint is server-asserted by api-gateway as sha256(UA + IP).
// Always present on requests that passed through the gateway. Empty means
// either the request bypassed the gateway or the gateway did not run the
// DeviceMiddleware (e.g., direct service-to-service calls in tests).

package middleware

import (
	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

// DeviceIDFromContext returns the X-Device-Id value off the incoming Echo
// request, or "" if absent or stripped by the gateway. Callers must treat
// empty as "no client-supplied device".
func DeviceIDFromContext(c echo.Context) string {
	return c.Request().Header.Get(auth.HeaderDeviceID)
}

// DeviceFingerprintFromContext returns the X-Device-Fingerprint injected by
// api-gateway, or "" if the request did not pass through the gateway. Callers
// that fall back DeviceID → Fingerprint should treat both empty as
// "completely anonymous, no per-device dedup possible".
func DeviceFingerprintFromContext(c echo.Context) string {
	return c.Request().Header.Get(auth.HeaderDeviceFingerprint)
}
