package middleware

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// operationalRoutes are skipped by every observability middleware in this
// package (§3.1). Otherwise every 15s scrape and every kubelet probe mints
// request-rate series that swamp the RED panels and log lines that swamp Loki,
// and 14 repos would each decide this differently.
//
// sale-service adds /up and its per-vertical */health on the PHP side; those
// are not Echo routes and never reach this code.
var operationalRoutes = map[string]struct{}{
	"/metrics": {},
	"/health":  {},
	"/livez":   {},
	"/readyz":  {},
}

// isOperationalRoute reports whether the request targets one of the skipped
// operational routes. Both the matched route template and the raw request path
// are checked: an unmounted probe path (e.g. a kubelet polling /livez on a
// service that does not serve it) matches no route, and without the raw-path
// check every such poll would land in the "unmatched" bucket.
func isOperationalRoute(c echo.Context) bool {
	if _, ok := operationalRoutes[c.Path()]; ok {
		return true
	}
	_, ok := operationalRoutes[c.Request().URL.Path]
	return ok
}

// routeTemplate returns the Echo route template for the request, clamped to
// metrics.RouteUnmatched when the router matched nothing. Echo leaves
// Context.Path() empty in that case, so the detection is exact rather than
// heuristic.
//
// The same value is used as a metric label, a log field and a span-name
// component: raw URLs carry uuids, which makes them both a cardinality bomb
// and a PII leak (§3.6, §9.18).
func routeTemplate(c echo.Context) string {
	if path := c.Path(); path != "" {
		return path
	}
	return metrics.RouteUnmatched
}

// responseStatus resolves the status code a request actually ends with.
//
// Echo runs its HTTPErrorHandler in ServeHTTP, i.e. AFTER the middleware chain
// returns, so a handler that returns an error rather than writing a response
// leaves Response().Status at its 200 default while the client receives the
// error's code. Reading the code off the returned error closes that gap
// without invoking the error handler ourselves (which would change response
// semantics for every service at once).
func responseStatus(c echo.Context, err error) int {
	if err == nil || c.Response().Committed {
		return c.Response().Status
	}

	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}

	var appErr *commonErrors.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}

	return http.StatusInternalServerError
}
