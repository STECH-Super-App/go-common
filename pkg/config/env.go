// Package config provides configuration utilities.
package config

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
)

// Init loads environment variables from .env files.
// If no filenames are provided, it loads .env by default.
func Init(filenames ...string) error {
	return godotenv.Load(filenames...)
}

// GetEnv retrieves the value of the environment variable named by the key.
// A variable that is unset OR set to the empty string counts as absent: the
// default is returned when one is given, "" otherwise. (An empty value used to
// be returned as is, so a ConfigMap key rendered empty replaced a default —
// an empty broker list — instead of leaving it; use os.LookupEnv where an
// explicitly empty value must mean something.)
func GetEnv(key string, defaultVal ...string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return ""
}

// InvalidHandler is told when a variable IS set but its value does not parse as
// the type its getter wants, and the default was used instead. want names the
// type ("int", "duration", "bool", ...). The value is never passed: it may be a
// secret.
type InvalidHandler func(key, want string)

var invalidHandler atomic.Pointer[InvalidHandler]

// SetInvalidHandler replaces the handler for unparseable values and returns the
// previous one. The default writes one JSON warn line to stderr naming the key
// and the wanted type. Passing nil restores the default.
//
// Configuration is read before the logger exists, which is why this is a hook
// and not a zap call.
func SetInvalidHandler(h InvalidHandler) InvalidHandler {
	prev := currentInvalidHandler()
	if h == nil {
		invalidHandler.Store(nil)
	} else {
		invalidHandler.Store(&h)
	}
	return prev
}

func currentInvalidHandler() InvalidHandler {
	if p := invalidHandler.Load(); p != nil {
		return *p
	}
	return func(key, want string) { reportTo(os.Stderr, key, want) }
}

// reportTo writes the default warn line: JSON, one line, so the container log
// pipeline parses it like every other service log line.
func reportTo(w io.Writer, key, want string) {
	_, _ = fmt.Fprintf(w, `{"level":"warn","msg":"config: environment variable is set but not a valid value; using the default","key":%q,"want":%q}`+"\n", key, want)
}

// parsed reads key, parses it with parse and returns the result; an absent or
// empty variable returns def silently, a set value that does not parse returns
// def after telling the InvalidHandler (SL-23).
func parsed[T any](key, want string, def T, parse func(string) (T, error)) T {
	raw := GetEnv(key)
	if raw == "" {
		return def
	}
	v, err := parse(raw)
	if err != nil {
		currentInvalidHandler()(key, want)
		return def
	}
	return v
}

func parseInt(bits int) func(string) (int64, error) {
	return func(s string) (int64, error) { return strconv.ParseInt(s, 10, bits) }
}

func parseUint(bits int) func(string) (uint64, error) {
	return func(s string) (uint64, error) { return strconv.ParseUint(s, 10, bits) }
}

// GetEnvInt retrieves the value of the environment variable named by the key and converts it to an int.
// If the variable is absent or empty the default is returned; if it is set but cannot be converted the default is returned and the InvalidHandler is told.
func GetEnvInt(key string, defaultVal int) int {
	return parsed(key, "int", defaultVal, strconv.Atoi)
}

// GetEnvInt8 is GetEnvInt for an int8.
func GetEnvInt8(key string, defaultVal int8) int8 {
	return int8(parsed(key, "int8", int64(defaultVal), parseInt(8))) //nolint:gosec // bounded by ParseInt's bitSize, or the int8 default
}

// GetEnvInt16 is GetEnvInt for an int16.
func GetEnvInt16(key string, defaultVal int16) int16 {
	return int16(parsed(key, "int16", int64(defaultVal), parseInt(16))) //nolint:gosec // bounded by ParseInt's bitSize, or the int16 default
}

// GetEnvInt32 is GetEnvInt for an int32.
func GetEnvInt32(key string, defaultVal int32) int32 {
	return int32(parsed(key, "int32", int64(defaultVal), parseInt(32))) //nolint:gosec // bounded by ParseInt's bitSize, or the int32 default
}

// GetEnvInt64 is GetEnvInt for an int64.
func GetEnvInt64(key string, defaultVal int64) int64 {
	return parsed(key, "int64", defaultVal, parseInt(64))
}

// GetEnvUint is GetEnvInt for a uint.
func GetEnvUint(key string, defaultVal uint) uint {
	return uint(parsed(key, "uint", uint64(defaultVal), parseUint(0)))
}

// GetEnvUint8 is GetEnvInt for a uint8.
func GetEnvUint8(key string, defaultVal uint8) uint8 {
	return uint8(parsed(key, "uint8", uint64(defaultVal), parseUint(8))) //nolint:gosec // bounded by ParseUint's bitSize, or the uint8 default
}

// GetEnvUint16 is GetEnvInt for a uint16.
func GetEnvUint16(key string, defaultVal uint16) uint16 {
	return uint16(parsed(key, "uint16", uint64(defaultVal), parseUint(16))) //nolint:gosec // bounded by ParseUint's bitSize, or the uint16 default
}

// GetEnvUint32 is GetEnvInt for a uint32.
func GetEnvUint32(key string, defaultVal uint32) uint32 {
	return uint32(parsed(key, "uint32", uint64(defaultVal), parseUint(32))) //nolint:gosec // bounded by ParseUint's bitSize, or the uint32 default
}

// GetEnvUint64 is GetEnvInt for a uint64.
func GetEnvUint64(key string, defaultVal uint64) uint64 {
	return parsed(key, "uint64", defaultVal, parseUint(64))
}

// GetEnvFloat32 is GetEnvInt for a float32.
func GetEnvFloat32(key string, defaultVal float32) float32 {
	return float32(parsed(key, "float32", float64(defaultVal), func(s string) (float64, error) {
		return strconv.ParseFloat(s, 32)
	}))
}

// GetEnvFloat is GetEnvInt for a float64.
func GetEnvFloat(key string, defaultVal float64) float64 {
	return parsed(key, "float64", defaultVal, func(s string) (float64, error) {
		return strconv.ParseFloat(s, 64)
	})
}

// GetEnvBool is GetEnvInt for a bool (strconv.ParseBool spellings: 1, t, T,
// TRUE, true, True, 0, f, F, FALSE, false, False — not "yes" or "on").
func GetEnvBool(key string, defaultVal bool) bool {
	return parsed(key, "bool", defaultVal, strconv.ParseBool)
}

// GetEnvDuration is GetEnvInt for a time.Duration; the value needs a unit
// ("30s", "5m"): a bare "30" does not parse.
func GetEnvDuration(key string, defaultVal time.Duration) time.Duration {
	return parsed(key, "duration", defaultVal, time.ParseDuration)
}
