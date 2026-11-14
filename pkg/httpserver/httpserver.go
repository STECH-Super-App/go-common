// Package httpserver builds the fleet's Echo server: the one place that sets
// the request-body limit, the net/http server timeouts, panic recovery, the
// standard error envelope and the standard observability middleware chain, so a
// service no longer assembles them by hand — and no longer forgets one.
//
// A bare echo.New() has no body limit (a client can stream gigabytes into a JSON
// bind), no ReadHeaderTimeout (a slowloris holds a connection per socket for
// ever), the default HTTPErrorHandler (router 404/405 answer {"message":…}, not
// the response envelope) and an IPExtractor that believes a client-sent
// X-Forwarded-For. This constructor fixes each of those. The lint guard in
// golangci-guards.yml bans the bare echo.New() outside this package once a
// service has adopted it.
//
//	e := httpserver.New(httpserver.Config{Logger: zlog})
//	health.Register(e)
//	app.HTTP("http", e, ":8080")   // pkg/lifecycle
package httpserver

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/clientip"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
	"github.com/STECH-Super-App/go-common/pkg/requestid"
	"github.com/STECH-Super-App/go-common/pkg/response"
)

// Defaults applied for every zero Config field.
const (
	// DefaultBodyLimit is 1 MiB: a JSON API body. A service that takes uploads
	// states a larger limit for itself.
	DefaultBodyLimit int64 = 1 << 20
	// DefaultReadHeaderTimeout bounds how long a client may take to send its
	// request headers (slowloris).
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultReadTimeout bounds reading the whole request, body included.
	DefaultReadTimeout = 60 * time.Second
	// DefaultIdleTimeout bounds a keep-alive connection waiting for its next
	// request.
	DefaultIdleTimeout = 120 * time.Second
	// DefaultMaxHeaderBytes is net/http's own default, written down.
	DefaultMaxHeaderBytes = 1 << 20
)

// Config tunes New. The zero value is the house default.
type Config struct {
	// Logger receives the access lines and recovered panics. Nil logs nothing.
	Logger *zap.Logger

	// BodyLimit is the largest request body in bytes (default DefaultBodyLimit).
	// An over-limit request is a 413 COMMON_REQUEST_TOO_LARGE.
	BodyLimit int64

	ReadHeaderTimeout time.Duration // default DefaultReadHeaderTimeout
	ReadTimeout       time.Duration // default DefaultReadTimeout
	IdleTimeout       time.Duration // default DefaultIdleTimeout
	MaxHeaderBytes    int           // default DefaultMaxHeaderBytes

	// WriteTimeout bounds writing the response. Its default is NONE, on purpose:
	// a server-wide write deadline kills every SSE and streaming response. A
	// service with no streams sets one; one with a few sets it per request with
	// http.NewResponseController(w).SetWriteDeadline.
	WriteTimeout time.Duration
}

// New returns an Echo server with the house defaults:
//
//   - net/http timeouts and header size as in Config; no banner, no port line;
//   - the client address is the TCP peer (echo.ExtractIPDirect): a client-sent
//     X-Forwarded-For or X-Real-IP is not believed (the trusted client address
//     is the gateway's job — spec §3.3);
//   - HTTPErrorHandler renders every error, router 404/405 and failed binds
//     included, through response.JSONError — the standard envelope with a
//     COMMON_* Reason;
//   - the validated X-Client-IP, when present, is put on the request context
//     (clientip.Middleware; read it with clientip.FromContext or FromEcho);
//   - middleware, outermost first: Tracing, requestid, clientip, RequestLogger,
//     Metrics, Locale, body limit, panic recovery. Recovery sits INSIDE the logger and
//     the metrics so a panic is still one access line and one 5xx observation.
func New(cfg Config) *echo.Echo {
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}

	e := echo.New() //nolint:forbidigo // this IS the constructor the guard sends everyone to
	e.HideBanner = true
	e.HidePort = true
	e.IPExtractor = echo.ExtractIPDirect()
	e.HTTPErrorHandler = errorHandler

	e.Server.ReadHeaderTimeout = orDuration(cfg.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	e.Server.ReadTimeout = orDuration(cfg.ReadTimeout, DefaultReadTimeout)
	e.Server.IdleTimeout = orDuration(cfg.IdleTimeout, DefaultIdleTimeout)
	e.Server.WriteTimeout = cfg.WriteTimeout
	e.Server.MaxHeaderBytes = DefaultMaxHeaderBytes
	if cfg.MaxHeaderBytes > 0 {
		e.Server.MaxHeaderBytes = cfg.MaxHeaderBytes
	}

	limit := cfg.BodyLimit
	if limit <= 0 {
		limit = DefaultBodyLimit
	}

	e.Use(
		middleware.Tracing(),
		requestid.Middleware(),
		clientip.Middleware(),
		middleware.RequestLogger(log),
		middleware.Metrics(),
		middleware.LocaleMiddleware(),
		bodyLimit(limit),
		recoverPanics(),
	)
	return e
}

// bodyLimit refuses a request body over limit bytes: at once from Content-Length
// when the client states it, and while reading otherwise (chunked bodies). The
// read-time error is an *http.MaxBytesError, which response.JSONError renders as
// the same 413. It is written here rather than taken from echo/middleware, whose
// package would add golang.org/x/time to every service's go.sum.
func bodyLimit(limit int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if req.ContentLength > limit {
				return echo.ErrStatusRequestEntityTooLarge
			}
			req.Body = http.MaxBytesReader(c.Response(), req.Body, limit)
			return next(c)
		}
	}
}

func orDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// errorHandler is Echo's HTTPErrorHandler: everything goes through JSONError.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	_ = response.JSONError(c, err)
}

// recoverPanics turns a handler panic into a logged, typed 500. The panic value
// is logged with its stack and never sent to the client. http.ErrAbortHandler is
// re-panicked: it is net/http's deliberate "abort this response" signal.
func recoverPanics() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				if r == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(r)
				}
				logger.FromContext(c.Request().Context()).Error("panic recovered",
					zap.Any("panic", r),
					zap.ByteString("stack", debug.Stack()),
				)
				err = commonErrors.New(http.StatusInternalServerError).
					Reason("COMMON_INTERNAL").
					Message("internal server error").
					Build()
			}()
			return next(c)
		}
	}
}
