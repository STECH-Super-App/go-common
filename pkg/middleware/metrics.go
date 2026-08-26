package middleware

import (
	"time"

	"github.com/labstack/echo/v4"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// Metrics returns the fleet-wide HTTP server metrics middleware. It observes
// metrics.HTTPRequestDuration — http_request_duration_seconds{method, route,
// status_class} — on every non-operational request (§3.1).
//
// It is a native echo.MiddlewareFunc, never echo.WrapMiddleware around a
// stdlib handler, for two reasons: Context.Path() (the route template) exists
// only on the Echo context, and wrapping http.ResponseWriter to capture the
// status would drop the http.Flusher implementation and break the SSE
// endpoints. Long-lived streams land in the +Inf duration bucket by design.
//
// The instrument is registered at package init in pkg/metrics, so calling
// Metrics() any number of times in one process is safe (§3.0 once-guard).
//
// Wire it early in the chain, and after Tracing() when tracing is enabled, so
// the recorded duration covers the handler rather than the rest of the chain.
func Metrics() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if isOperationalRoute(c) {
				return next(c)
			}

			start := time.Now()
			err := next(c)

			metrics.HTTPRequestDuration.WithLabelValues(
				c.Request().Method,
				routeTemplate(c),
				metrics.StatusClass(responseStatus(c, err)),
			).Observe(time.Since(start).Seconds())

			return err
		}
	}
}
