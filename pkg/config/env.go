// Package config provides configuration utilities.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Init loads environment variables from .env files.
// If no filenames are provided, it loads .env by default.
func Init(filenames ...string) error {
	return godotenv.Load(filenames...)
}

// GetEnv retrieves the value of the environment variable named by the key.
// It returns the value, which will be empty if the variable is not present.
// If a default value is provided and the environment variable is empty, the default value is returned.
func GetEnv(key string, defaultVal ...string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return ""
}

// GetEnvInt retrieves the value of the environment variable named by the key and converts it to an int.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvInt(key string, defaultVal int) int {
	valueStr := GetEnv(key)
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultVal
}

// GetEnvInt8 retrieves the value of the environment variable named by the key and converts it to an int8.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvInt8(key string, defaultVal int8) int8 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseInt(valueStr, 10, 8); err == nil {
		return int8(value)
	}
	return defaultVal
}

// GetEnvInt16 retrieves the value of the environment variable named by the key and converts it to an int16.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvInt16(key string, defaultVal int16) int16 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseInt(valueStr, 10, 16); err == nil {
		return int16(value)
	}
	return defaultVal
}

// GetEnvInt32 retrieves the value of the environment variable named by the key and converts it to an int32.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvInt32(key string, defaultVal int32) int32 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseInt(valueStr, 10, 32); err == nil {
		return int32(value)
	}
	return defaultVal
}

// GetEnvInt64 retrieves the value of the environment variable named by the key and converts it to an int64.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvInt64(key string, defaultVal int64) int64 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
		return value
	}
	return defaultVal
}

// GetEnvUint retrieves the value of the environment variable named by the key and converts it to a uint.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvUint(key string, defaultVal uint) uint {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseUint(valueStr, 10, 0); err == nil {
		return uint(value)
	}
	return defaultVal
}

// GetEnvUint8 retrieves the value of the environment variable named by the key and converts it to a uint8.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvUint8(key string, defaultVal uint8) uint8 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseUint(valueStr, 10, 8); err == nil {
		return uint8(value)
	}
	return defaultVal
}

// GetEnvUint16 retrieves the value of the environment variable named by the key and converts it to a uint16.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvUint16(key string, defaultVal uint16) uint16 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseUint(valueStr, 10, 16); err == nil {
		return uint16(value)
	}
	return defaultVal
}

// GetEnvUint32 retrieves the value of the environment variable named by the key and converts it to a uint32.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvUint32(key string, defaultVal uint32) uint32 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseUint(valueStr, 10, 32); err == nil {
		return uint32(value)
	}
	return defaultVal
}

// GetEnvUint64 retrieves the value of the environment variable named by the key and converts it to a uint64.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvUint64(key string, defaultVal uint64) uint64 {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseUint(valueStr, 10, 64); err == nil {
		return value
	}
	return defaultVal
}

// GetEnvFloat32 retrieves the value of the environment variable named by the key and converts it to a float32.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvFloat32(key string, defaultVal float32) float32 {
	val := GetEnv(key)
	if val == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(val, 32)
	if err != nil {
		return defaultVal
	}
	return float32(f)
}

// GetEnvFloat retrieves the value of the environment variable named by the key and converts it to a float64.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvFloat(key string, defaultVal float64) float64 {
	val := GetEnv(key)
	if val == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// GetEnvBool retrieves the value of the environment variable named by the key and converts it to a bool.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvBool(key string, defaultVal bool) bool {
	valueStr := GetEnv(key)
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultVal
}

// GetEnvDuration retrieves the value of the environment variable named by the key and converts it to a time.Duration.
// If the variable is not present or cannot be converted, the default value is returned.
func GetEnvDuration(key string, defaultVal time.Duration) time.Duration {
	valueStr := GetEnv(key)
	if value, err := time.ParseDuration(valueStr); err == nil {
		return value
	}
	return defaultVal
}
