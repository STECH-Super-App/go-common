package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

// recordingTracer installs a real SDK provider plus the W3C propagator for the
// duration of a test and restores the globals afterwards.
func recordingTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))

	prevProvider := otel.GetTracerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
	})

	return exporter
}

func attrOf(t *testing.T, attrs []attribute.KeyValue, key string) attribute.Value {
	t.Helper()
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	t.Fatalf("span carries no %q attribute", key)
	return attribute.Value{}
}

// The span name is "METHOD route-template" — never the raw URL. A span name
// with a uuid in it is the §3.6 cardinality violation wearing tracing clothes
// (trap §9.18).
func TestTracing_SpanNameIsMethodPlusRouteTemplate(t *testing.T) {
	exporter := recordingTracer(t)

	e := echo.New()
	e.Use(middleware.Tracing())
	e.GET("/orders/:id", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	e.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/orders/11111111-1111-1111-1111-111111111111", nil))

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	assert.Equal(t, "GET /orders/:id", spans[0].Name)
	assert.Equal(t, trace.SpanKindServer, spans[0].SpanKind)
	assert.EqualValues(t, http.StatusOK, attrOf(t, spans[0].Attributes, "http.status_code").AsInt64())
	assert.Equal(t, "/orders/:id", attrOf(t, spans[0].Attributes, "http.route").AsString())
}

func TestTracing_UnmatchedRouteClamped(t *testing.T) {
	exporter := recordingTracer(t)

	e := echo.New()
	e.Use(middleware.Tracing())

	e.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/wp-admin/setup-config.php", nil))

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET unmatched", spans[0].Name)
	assert.EqualValues(t, http.StatusNotFound, attrOf(t, spans[0].Attributes, "http.status_code").AsInt64())
}

// An inbound traceparent must become the REMOTE PARENT of the server span,
// which is what stitches the gateway hop to the service hop.
func TestTracing_ExtractsInboundTraceparent(t *testing.T) {
	exporter := recordingTracer(t)

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	parentSpanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	e := echo.New()
	e.Use(middleware.Tracing())
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	e.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, traceID, spans[0].Parent.TraceID())
	assert.Equal(t, parentSpanID, spans[0].Parent.SpanID())
	assert.True(t, spans[0].Parent.IsRemote())
}

func TestTracing_SkipsOperationalRoutes(t *testing.T) {
	exporter := recordingTracer(t)

	e := echo.New()
	e.Use(middleware.Tracing())
	e.GET("/health", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Empty(t, exporter.GetSpans(), "probe traffic must not produce spans")
}

// With no provider configured (tracing.Init found no endpoint) the middleware
// must still be a working pass-through: absence-tolerance, trap §9.16.
func TestTracing_NoOpProviderStillServesRequests(t *testing.T) {
	e := echo.New()
	e.Use(middleware.Tracing())
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}
