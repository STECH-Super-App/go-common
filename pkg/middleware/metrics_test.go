package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

// histCount returns the observation count of the histogram sample carrying
// exactly the given labels, or 0 when no such sample exists yet. Counting
// samples (rather than reading a gauge) is what lets these tests assert on
// deltas against a process-global registry.
func histCount(t *testing.T, family string, labels map[string]string) uint64 {
	t.Helper()

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			if matchLabels(m, labels) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func matchLabels(m *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

const httpDurationFamily = "http_request_duration_seconds"

// Test (a): the middleware records the Echo route TEMPLATE, clamps an
// unmatched route to "unmatched" (§3.1 / trap §9.3), and skips the four
// operational routes.
func TestMetrics_RouteTemplateAndUnmatchedClamp(t *testing.T) {
	e := echo.New()
	e.Use(middleware.Metrics())
	e.GET("/users/:id", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	templateLabels := map[string]string{"method": "GET", "route": "/users/:id", "status_class": "2xx"}
	before := histCount(t, httpDurationFamily, templateLabels)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/11111111-1111-1111-1111-111111111111", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, before+1, histCount(t, httpDurationFamily, templateLabels),
		"route label must be the template, never the raw URL")

	// The raw path must never appear as a label value (§3.6 cardinality budget).
	assert.Zero(t, histCount(t, httpDurationFamily, map[string]string{
		"method": "GET", "route": "/users/11111111-1111-1111-1111-111111111111", "status_class": "2xx",
	}))
}

func TestMetrics_UnmatchedRouteClamped(t *testing.T) {
	e := echo.New()
	e.Use(middleware.Metrics())
	e.GET("/known", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	unmatched := map[string]string{"method": "GET", "route": "unmatched", "status_class": "4xx"}
	before := histCount(t, httpDurationFamily, unmatched)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-admin/setup-config.php", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)

	assert.Equal(t, before+1, histCount(t, httpDurationFamily, unmatched),
		`a scanner 404 must clamp to route="unmatched" and status_class="4xx"`)
}

func TestMetrics_SkipsOperationalRoutes(t *testing.T) {
	for _, path := range []string{"/metrics", "/health", "/livez", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			e := echo.New()
			e.Use(middleware.Metrics())
			e.GET(path, func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

			labels := map[string]string{"method": "GET", "route": path, "status_class": "2xx"}
			before := histCount(t, httpDurationFamily, labels)

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, rec.Code)

			assert.Equal(t, before, histCount(t, httpDurationFamily, labels),
				"operational routes must mint no series")
		})
	}
}

// An operational path that is NOT registered as a route must be skipped too:
// an unmounted /health probe would otherwise land in the "unmatched" bucket
// on every kubelet poll.
func TestMetrics_SkipsOperationalRoutesWhenUnrouted(t *testing.T) {
	e := echo.New()
	e.Use(middleware.Metrics())

	unmatched := map[string]string{"method": "GET", "route": "unmatched", "status_class": "4xx"}
	before := histCount(t, httpDurationFamily, unmatched)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)

	assert.Equal(t, before, histCount(t, httpDurationFamily, unmatched))
}

// Test (b): status_class is bucketed from c.Response().Status after next(c),
// including the case where the handler returns an error instead of writing.
func TestMetrics_StatusClassBuckets(t *testing.T) {
	cases := []struct {
		name    string
		route   string
		handler echo.HandlerFunc
		want    string
	}{
		{"2xx", "/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") }, "2xx"},
		{"3xx", "/moved", func(c echo.Context) error { return c.Redirect(http.StatusMovedPermanently, "/ok") }, "3xx"},
		{"4xx-written", "/bad", func(c echo.Context) error { return c.String(http.StatusBadRequest, "bad") }, "4xx"},
		{"4xx-returned", "/forbidden", func(_ echo.Context) error { return echo.NewHTTPError(http.StatusForbidden) }, "4xx"},
		{"5xx-written", "/boom", func(c echo.Context) error { return c.String(http.StatusInternalServerError, "boom") }, "5xx"},
		{"5xx-returned", "/panic", func(_ echo.Context) error { return echo.NewHTTPError(http.StatusBadGateway) }, "5xx"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			e.Use(middleware.Metrics())
			e.GET(tc.route, tc.handler)

			labels := map[string]string{"method": "GET", "route": tc.route, "status_class": tc.want}
			before := histCount(t, httpDurationFamily, labels)

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.route, nil))

			assert.Equal(t, before+1, histCount(t, httpDurationFamily, labels),
				"expected status_class=%s for %s", tc.want, tc.name)
		})
	}
}

// Test (c), half two: the once-guard. Building the middleware repeatedly in
// one process must not panic — service tests rebuild their whole HTTP server
// per test case (§3.0, trap §9.4).
func TestMetrics_BuiltTwiceDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		for i := 0; i < 3; i++ {
			e := echo.New()
			e.Use(middleware.Metrics())
			e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
		}
	})
}
