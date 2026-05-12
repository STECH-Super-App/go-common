package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestDeviceIDFromContext(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"present", "abc-123", "abc-123"},
		{"absent", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set(auth.HeaderDeviceID, tc.header)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			assert.Equal(t, tc.want, DeviceIDFromContext(c))
		})
	}
}

func TestDeviceFingerprintFromContext(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"present", "0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"},
		{"absent", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set(auth.HeaderDeviceFingerprint, tc.header)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			assert.Equal(t, tc.want, DeviceFingerprintFromContext(c))
		})
	}
}
