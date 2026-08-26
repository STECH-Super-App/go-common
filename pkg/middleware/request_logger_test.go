package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

func observedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.InfoLevel)
	return zap.New(core), logs
}

func fieldsOf(t *testing.T, entry observer.LoggedEntry) map[string]any {
	t.Helper()
	return entry.ContextMap()
}

// Test (g), part one: the middleware puts a logger on the REQUEST context
// (not just the echo context) carrying request_id, reachable from any layer
// that holds only a context.Context.
func TestRequestLogger_ContextLoggerCarriesRequestID(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base))
	e.GET("/orders/:id", func(c echo.Context) error {
		// This is what a repository three layers down would do.
		logger.FromContext(c.Request().Context()).Info("inner work")
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	req.Header.Set(middleware.HeaderRequestID, "req-abc-123")
	e.ServeHTTP(httptest.NewRecorder(), req)

	inner := logs.FilterMessage("inner work").All()
	require.Len(t, inner, 1, "the context logger must be reachable via logger.FromContext")
	assert.Equal(t, "req-abc-123", fieldsOf(t, inner[0])["request_id"])
}

// Test (g), part two: trace_id/span_id appear on the context logger exactly
// when a span is active — this is the logs↔traces join Grafana pivots on.
func TestRequestLogger_AddsTraceFieldsWhenSpanActive(t *testing.T) {
	base, logs := observedLogger()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})

	e := echo.New()
	e.Use(middleware.RequestLogger(base))
	e.GET("/ok", func(c echo.Context) error {
		logger.FromContext(c.Request().Context()).Info("inner work")
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(middleware.HeaderRequestID, "req-1")
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), spanCtx))
	e.ServeHTTP(httptest.NewRecorder(), req)

	inner := logs.FilterMessage("inner work").All()
	require.Len(t, inner, 1)

	f := fieldsOf(t, inner[0])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", f["trace_id"])
	assert.Equal(t, "00f067aa0ba902b7", f["span_id"])
}

func TestRequestLogger_NoTraceFieldsWithoutSpan(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base))
	e.GET("/ok", func(c echo.Context) error {
		logger.FromContext(c.Request().Context()).Info("inner work")
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(middleware.HeaderRequestID, "req-1")
	e.ServeHTTP(httptest.NewRecorder(), req)

	f := fieldsOf(t, logs.FilterMessage("inner work").All()[0])
	assert.NotContains(t, f, "trace_id", "an invalid span context must not write all-zero ids")
	assert.NotContains(t, f, "span_id")
}

// Exactly ONE access line per request, with the §3b field set.
func TestRequestLogger_EmitsOneAccessLine(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base))
	e.GET("/orders/:id", func(c echo.Context) error { return c.String(http.StatusCreated, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	req.Header.Set(middleware.HeaderRequestID, "req-abc-123")
	e.ServeHTTP(httptest.NewRecorder(), req)

	access := logs.FilterMessage("http request").All()
	require.Len(t, access, 1, "exactly one access line per request")

	f := fieldsOf(t, access[0])
	assert.Equal(t, "req-abc-123", f["request_id"])
	assert.Equal(t, "/orders/:id", f["route"], "route is the template, never the raw URL")
	assert.Equal(t, "GET", f["method"])
	assert.EqualValues(t, http.StatusCreated, f["status"])
	assert.Contains(t, f, "duration_ms")
	assert.NotContains(t, f, "upstream", "upstream is gateway-only, off by default")
}

func TestRequestLogger_UnmatchedRouteClamped(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base))

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/wp-admin/setup-config.php", nil))

	access := logs.FilterMessage("http request").All()
	require.Len(t, access, 1)

	f := fieldsOf(t, access[0])
	assert.Equal(t, "unmatched", f["route"])
	assert.EqualValues(t, http.StatusNotFound, f["status"])
}

func TestRequestLogger_WithUpstream(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base, middleware.WithUpstream(func(_ echo.Context) string {
		return "order-service"
	})))
	e.GET("/order/*", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/order/requests", nil))

	access := logs.FilterMessage("http request").All()
	require.Len(t, access, 1)
	assert.Equal(t, "order-service", fieldsOf(t, access[0])["upstream"])
}

func TestRequestLogger_SkipsOperationalRoutes(t *testing.T) {
	for _, path := range []string{"/metrics", "/health", "/livez", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			base, logs := observedLogger()

			e := echo.New()
			e.Use(middleware.RequestLogger(base))
			e.GET(path, func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

			e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))

			assert.Empty(t, logs.All(), "probe traffic must not produce access lines")
		})
	}
}

func TestRequestLogger_NoRequestIDHeader(t *testing.T) {
	base, logs := observedLogger()

	e := echo.New()
	e.Use(middleware.RequestLogger(base))
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))

	access := logs.FilterMessage("http request").All()
	require.Len(t, access, 1)
	assert.NotContains(t, fieldsOf(t, access[0]), "request_id",
		"the gateway is the single stamping point; a service must not mint its own id")
}

func TestRequestLogger_NilBaseDoesNotPanic(t *testing.T) {
	e := echo.New()
	e.Use(middleware.RequestLogger(nil))
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	require.NotPanics(t, func() {
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	})
}
