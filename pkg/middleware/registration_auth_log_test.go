package middleware_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

// SL-12 (= ID-11): RegistrationAuthMiddleware wrote the caller's phone number
// to the standard logger on every request — PII in Loki, in a non-JSON line.
func TestRegistrationAuthMiddleware_NeverLogsThePhone(t *testing.T) {
	const phone = "+79991234567"

	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) }()

	e := echo.New()
	handler := middleware.RegistrationAuthMiddleware(func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/register", nil)
	req.Header.Set(auth.HeaderPhone, phone)
	req.Header.Set(auth.HeaderScope, "registration")
	rec := httptest.NewRecorder()

	require.NoError(t, handler(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.NotContains(t, buf.String(), phone, "the phone number must not reach any log line")
	assert.Empty(t, buf.String(), "the middleware must not write to the standard logger at all")
}
