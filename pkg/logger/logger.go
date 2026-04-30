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

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	pkgOnce   sync.Once
	pkgLogger *zap.Logger
)

// New creates a new zap.Logger based on the provided log level.
// Level can be "debug", "info", "warn", "error". Default is "info".
func New(level string) (*zap.Logger, error) {
	cfg := buildConfig(level)
	return cfg.Build()
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

func buildConfig(level string) zap.Config {
	var cfg zap.Config
	if os.Getenv("APP_ENV") == "production" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}
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
