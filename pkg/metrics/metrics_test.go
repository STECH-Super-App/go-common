package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// TestInstrumentsRegisteredOnSharedRegistry pins the §3.0 contract: the two
// labelled families are registered at package init on metrics.Registry, not
// on the prometheus default registry (trap §9.11) and not inside a
// constructor (trap §9.4).
func TestInstrumentsRegisteredOnSharedRegistry(t *testing.T) {
	// A re-Register that reports AlreadyRegistered is the exact proof of
	// init-time registration on THIS registry. Gathering would prove nothing:
	// a labelled vec with no observations yet exports no children at all.
	for _, c := range []struct {
		name       string
		instrument prometheus.Collector
	}{
		{"http_request_duration_seconds", metrics.HTTPRequestDuration},
		{"grpc_server_handling_seconds", metrics.GRPCServerHandling},
	} {
		err := metrics.Registry.Register(c.instrument)
		var already prometheus.AlreadyRegisteredError
		assert.ErrorAs(t, err, &already,
			"%s must be registered on metrics.Registry at package init", c.name)
	}
}

// TestDefaultBuckets pins the §3.1 bucket list verbatim — dashboards and alert
// expressions are written against these boundaries.
func TestDefaultBuckets(t *testing.T) {
	assert.Equal(t,
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		metrics.DefaultBuckets)
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		200: "2xx",
		201: "2xx",
		301: "3xx",
		400: "4xx",
		404: "4xx",
		499: "4xx",
		500: "5xx",
		503: "5xx",
		100: "1xx",
	}
	for code, want := range cases {
		assert.Equal(t, want, metrics.StatusClass(code), "status %d", code)
	}
}

// Test (c), half one: MountOn must be safe on two different Echo instances in
// one process (§3.5) — service tests rebuild servers repeatedly.
func TestMountOn_TwoEchoInstancesNoPanic(t *testing.T) {
	require.NotPanics(t, func() {
		e1 := echo.New()
		e2 := echo.New()
		metrics.MountOn(e1)
		metrics.MountOn(e2)
		// Re-mounting on the same instance must not panic either.
		metrics.MountOn(e1)
	})

	// Give the vec a child so the family is actually exportable, then prove
	// the mounted endpoint serves the SHARED registry rather than a fresh one.
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/mount-on-probe", "2xx").Observe(0.01)

	e := echo.New()
	metrics.MountOn(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `http_request_duration_seconds_count{method="GET",route="/mount-on-probe",status_class="2xx"}`,
		"/metrics must expose the shared registry")
}

func TestStartServer_ServesAndShutsDown(t *testing.T) {
	stop := metrics.StartServer("127.0.0.1:0")
	require.NotNil(t, stop)

	// The listener address is not returned by the pinned signature, so this
	// asserts the lifecycle contract: shutdown is idempotent and never errors
	// on a healthy server.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, stop(ctx))
	require.NoError(t, stop(ctx))
}
