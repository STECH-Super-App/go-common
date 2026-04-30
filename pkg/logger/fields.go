package logger

import "go.uber.org/zap"

// String constructs a string field for structured logging.
// Re-exported so callers can write logger.String(...) without
// importing zap directly.
func String(key, value string) zap.Field {
	return zap.String(key, value)
}

// Int constructs an int field for structured logging.
func Int(key string, value int) zap.Field {
	return zap.Int(key, value)
}
