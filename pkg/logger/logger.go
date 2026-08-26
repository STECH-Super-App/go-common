// Package logger provides structured logging functionality.
//
// New constructs a configured *zap.Logger for application code that
// owns a logger instance. The package-level helpers (Warn, Info, Error
// plus the field constructors) are for cross-cutting library code in
// go-common that emits diagnostic logs without holding a logger of its
// own. Library code should prefer these helpers over fmt/log to keep
// log output structured.
package logger

import (
	"os"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	pkgOnce   sync.Once
	pkgLogger *zap.Logger

	// fallbackLogger holds the most recent logger built by New. FromContext
	// returns it when a context carries no request-scoped logger, so a line
	// logged outside a request still carries the service identity. Atomic
	// because services build their logger on the main goroutine while
	// handlers read it from many.
	fallbackLogger atomic.Pointer[zap.Logger]
)

// New creates a new zap.Logger at the given level, tagged with the service
// name that every line must carry.
//
// Level can be "debug", "info", "warn", "error"; anything else falls back to
// "info". The service argument is the workload's single observability identity
// — the same string used as the Prometheus scrape job and the Loki stream
// label (§3b). Pass a literal equal to the repo's scrape identity
// ("order-service", "api-gateway", …); a metrics panel and a log stream that
// disagree about a service's name cannot be pivoted between.
//
// The logger built here is also recorded as the process-wide fallback returned
// by FromContext when a context carries none.
func New(level, service string) (*zap.Logger, error) {
	l, err := buildConfig(level).Build()
	if err != nil {
		return nil, err
	}

	l = l.With(zap.String("service", service))
	fallbackLogger.Store(l)

	return l, nil
}

// pkg returns the package-level logger, initializing it lazily on first use.
// The level is taken from LOG_LEVEL (default "info").
func pkg() *zap.Logger {
	pkgOnce.Do(func() {
		level := os.Getenv("LOG_LEVEL")
		if level == "" {
			level = "info"
		}
		l, err := buildConfig(level).Build()
		if err != nil {
			// Fall back to a minimal no-op logger if construction fails.
			l = zap.NewNop()
		}
		pkgLogger = l
	})
	return pkgLogger
}

// buildConfig returns the fleet-wide logger configuration.
//
// The JSON encoder is UNCONDITIONAL — a container's log stream is not a
// developer's terminal, and a console line is unparseable by LogQL's `| json`,
// which takes out the level label, the request_id filter and the
// trace_id→Tempo join in one stroke (§3b). The previous
// APP_ENV=="production" gate never selected JSON anywhere: compose and both
// k8s overlays set "local" or "prod", so every service in every environment
// logged console output to stderr.
//
// Sampling is disabled deliberately. zap's production preset drops repeated
// entries with the same level+message after the first 100 per second, which
// would silently discard access lines — the one line per request that §3b
// makes the ops workhorse — under exactly the load where they matter most.
func buildConfig(level string) zap.Config {
	cfg := zap.NewProductionConfig()
	cfg.Sampling = nil
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	l, err := zapcore.ParseLevel(level)
	if err != nil {
		l = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(l)
	return cfg
}

// Warn logs at warn level on the package-level logger.
// Library code in go-common uses this for cross-cutting diagnostics
// (e.g., empty-reason warnings, locale fallbacks).
func Warn(msg string, fields ...zap.Field) {
	pkg().Warn(msg, fields...)
}

// Info logs at info level on the package-level logger.
func Info(msg string, fields ...zap.Field) {
	pkg().Info(msg, fields...)
}

// Error logs at error level on the package-level logger.
func Error(msg string, fields ...zap.Field) {
	pkg().Error(msg, fields...)
}
