// Package logger provides structured logging functionality.
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a new zap.Logger based on the provided log level.
// Level can be "debug", "info", "warn", "error". Default is "info".
func New(level string) (*zap.Logger, error) {
	var zapConfig zap.Config
	if os.Getenv("APP_ENV") == "production" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	l, err := zapcore.ParseLevel(level)
	if err != nil {
		l = zapcore.InfoLevel
	}
	zapConfig.Level = zap.NewAtomicLevelAt(l)

	return zapConfig.Build()
}
