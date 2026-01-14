package config

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	_ = os.Setenv("TEST_KEY", "value")
	defer func() { _ = os.Unsetenv("TEST_KEY") }()

	if got := GetEnv("TEST_KEY"); got != "value" {
		t.Errorf("GetEnv() = %v, want %v", got, "value")
	}

	if got := GetEnv("MISSING_KEY", "default"); got != "default" {
		t.Errorf("GetEnv() = %v, want %v", got, "default")
	}
}

func TestGetEnvInt(t *testing.T) {
	_ = os.Setenv("TEST_INT", "123")
	defer func() { _ = os.Unsetenv("TEST_INT") }()

	if got := GetEnvInt("TEST_INT", 0); got != 123 {
		t.Errorf("GetEnvInt() = %v, want %v", got, 123)
	}

	if got := GetEnvInt("MISSING_INT", 456); got != 456 {
		t.Errorf("GetEnvInt() = %v, want %v", got, 456)
	}
}
