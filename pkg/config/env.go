// Package config provides configuration utilities.
package config

import (
	"os"
	"strconv"
	"time"
)

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
