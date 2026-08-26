package metrics

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// Path is the exposition path served by both MountOn and StartServer.
const Path = "/metrics"

// readHeaderTimeout bounds the header-read phase of the standalone metrics
// listener (defence against Slowloris; gosec G112).
const readHeaderTimeout = 10 * time.Second

var (
	handlerOnce sync.Once
	handler     http.Handler
)

// exposition returns the promhttp handler for the shared Registry, built
// exactly once per process. Building it once is what makes MountOn and
// StartServer safe to call repeatedly and in combination (§3.0 once-guard).
func exposition() http.Handler {
	handlerOnce.Do(func() {
		handler = promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
			Registry:          Registry,
			EnableOpenMetrics: false,
		})
	})
	return handler
}

// MountOn wires GET /metrics on the given Echo instance, serving the shared
// Registry (§3.5).
//
// It is idempotent: safe to call on two different Echo instances in one
// process, and safe to call twice on the same instance (Echo replaces the
// route handler rather than panicking). Service tests that rebuild their HTTP
// server per case rely on both properties.
//
// Mounting on the public Echo is not sufficient by itself when the API gateway
// tunnels the prefix — see the exposure decision in the design spec §11.8.
func MountOn(e *echo.Echo) {
	e.GET(Path, echo.WrapHandler(exposition()))
}

// StartServer starts a standalone net/http listener serving GET /metrics on
// addr, for services with no public Echo and as the uniform exposure mechanism
// when metrics live on their own port. It returns a stop function that
// gracefully shuts the listener down; stop is safe to call more than once.
//
// A bind failure cannot be returned through the pinned signature, so it is
// logged at error level: observability must never take the process down.
func StartServer(addr string) (stop func(context.Context) error) {
	mux := http.NewServeMux()
	mux.Handle(Path, exposition())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server stopped unexpectedly",
				zap.String("addr", addr),
				zap.Error(err))
		}
	}()

	return srv.Shutdown
}
