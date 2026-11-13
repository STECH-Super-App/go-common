package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/STECH-Super-App/go-common/pkg/httpserver"
	"github.com/STECH-Super-App/go-common/pkg/requestid"
	"github.com/STECH-Super-App/go-common/pkg/response"
)

func do(e *echo.Echo, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) response.Error {
	t.Helper()
	var res response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res), "body: %s", rec.Body.String())
	require.NotNil(t, res.Error)
	return *res.Error
}

func TestNew_ServerTimeoutsAndLimitsHaveSaneDefaults(t *testing.T) {
	e := httpserver.New(httpserver.Config{})

	s := e.Server
	assert.Equal(t, httpserver.DefaultReadHeaderTimeout, s.ReadHeaderTimeout, "slowloris: headers must arrive in time")
	assert.Equal(t, httpserver.DefaultReadTimeout, s.ReadTimeout)
	assert.Equal(t, httpserver.DefaultIdleTimeout, s.IdleTimeout)
	assert.Equal(t, httpserver.DefaultMaxHeaderBytes, s.MaxHeaderBytes)
	assert.Zero(t, s.WriteTimeout, "no default write deadline: SSE and streams must keep working")
	assert.True(t, e.HideBanner)
	assert.True(t, e.HidePort)
}

func TestNew_ConfigOverridesDefaults(t *testing.T) {
	e := httpserver.New(httpserver.Config{
		ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second, MaxHeaderBytes: 4096,
	})

	assert.Equal(t, time.Second, e.Server.ReadHeaderTimeout)
	assert.Equal(t, 2*time.Second, e.Server.ReadTimeout)
	assert.Equal(t, 3*time.Second, e.Server.WriteTimeout)
	assert.Equal(t, 4*time.Second, e.Server.IdleTimeout)
	assert.Equal(t, 4096, e.Server.MaxHeaderBytes)
}

func TestNew_BodyOverTheLimitIs413WithTheStandardEnvelope(t *testing.T) {
	e := httpserver.New(httpserver.Config{BodyLimit: 16})
	e.POST("/echo", func(c echo.Context) error {
		var in map[string]any
		if err := c.Bind(&in); err != nil {
			return err
		}
		return response.Success(c, in)
	})

	small := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"a":1}`))
	small.Header.Set("Content-Type", "application/json")
	assert.Equal(t, http.StatusOK, do(e, small).Code)

	big := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`))
	big.Header.Set("Content-Type", "application/json")
	rec := do(e, big)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "COMMON_REQUEST_TOO_LARGE", errorOf(t, rec).Reason)
}

func TestNew_DefaultBodyLimitIsOneMiB(t *testing.T) {
	e := httpserver.New(httpserver.Config{})
	e.POST("/sink", func(c echo.Context) error {
		_, err := c.Request().Body.Read(make([]byte, 1))
		_ = err
		return response.Success(c, nil)
	})

	req := httptest.NewRequest(http.MethodPost, "/sink", bytes.NewReader(make([]byte, httpserver.DefaultBodyLimit+1)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, do(e, req).Code)
}

func TestNew_PanicBecomesATyped500AndTheServerSurvives(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	e := httpserver.New(httpserver.Config{Logger: zap.New(core)})
	e.GET("/boom", func(echo.Context) error { panic("kaboom") })
	e.GET("/ok", func(c echo.Context) error { return response.Success(c, nil) })

	rec := do(e, httptest.NewRequest(http.MethodGet, "/boom", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	out := errorOf(t, rec)
	assert.Equal(t, "COMMON_INTERNAL", out.Reason)
	assert.NotContains(t, rec.Body.String(), "kaboom", "a panic value never reaches a client")
	assert.Equal(t, http.StatusOK, do(e, httptest.NewRequest(http.MethodGet, "/ok", nil)).Code)

	var panicLogged, accessLogged bool
	for _, l := range logs.All() {
		if l.Message == "panic recovered" && l.Level == zapcore.ErrorLevel {
			panicLogged = true
			assert.NotEmpty(t, l.ContextMap()["stack"])
		}
		if l.Message == "http request" && l.ContextMap()["route"] == "/boom" {
			accessLogged = true
			assert.EqualValues(t, 500, l.ContextMap()["status"], "the access line must see the 500")
		}
	}
	assert.True(t, panicLogged, "the panic is logged with its stack")
	assert.True(t, accessLogged, "Recover sits inside the logger so the 500 is observed")
}

func TestNew_RouterErrorsUseTheStandardEnvelope(t *testing.T) {
	e := httpserver.New(httpserver.Config{})
	e.GET("/only-get", func(c echo.Context) error { return response.Success(c, nil) })

	rec := do(e, httptest.NewRequest(http.MethodGet, "/missing", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "COMMON_NOT_FOUND", errorOf(t, rec).Reason)

	rec = do(e, httptest.NewRequest(http.MethodPost, "/only-get", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "COMMON_METHOD_NOT_ALLOWED", errorOf(t, rec).Reason)
}

func TestNew_RequestIDIsStampedAndEchoed(t *testing.T) {
	e := httpserver.New(httpserver.Config{})
	e.GET("/ok", func(c echo.Context) error { return response.Success(c, nil) })

	rec := do(e, httptest.NewRequest(http.MethodGet, "/ok", nil))
	assert.True(t, requestid.Valid(rec.Header().Get(requestid.Header)), "a minted id is echoed on the response")

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(requestid.Header, "abc-123")
	assert.Equal(t, "abc-123", do(e, req).Header().Get(requestid.Header))
}

// The trusted client address is the gateway's job (spec §3.3): until it ships,
// a client-sent X-Forwarded-For must not be believed. Echo's default extractor
// reads it.
func TestNew_RealIPIgnoresClientSentForwardingHeaders(t *testing.T) {
	e := httpserver.New(httpserver.Config{})
	var got string
	e.GET("/ip", func(c echo.Context) error { got = c.RealIP(); return response.Success(c, nil) })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "10.1.2.3:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	req.Header.Set("X-Real-IP", "203.0.113.88")
	do(e, req)

	assert.Equal(t, "10.1.2.3", got)
}

func TestNew_NilLoggerIsAllowed(t *testing.T) {
	e := httpserver.New(httpserver.Config{})
	e.GET("/ok", func(c echo.Context) error { return response.Success(c, nil) })

	assert.Equal(t, http.StatusOK, do(e, httptest.NewRequest(http.MethodGet, "/ok", nil)).Code)
}

// A chunked body states no Content-Length, so only the read-time limit sees it.
func TestNew_ChunkedBodyOverTheLimitIs413(t *testing.T) {
	e := httpserver.New(httpserver.Config{BodyLimit: 16})
	e.POST("/echo", func(c echo.Context) error {
		var in map[string]any
		if err := c.Bind(&in); err != nil {
			return err
		}
		return response.Success(c, in)
	})
	req := httptest.NewRequest(http.MethodPost, "/echo", io.NopCloser(strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`)))
	req.ContentLength = -1 // unknown, as for Transfer-Encoding: chunked
	req.Header.Set("Content-Type", "application/json")

	rec := do(e, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "COMMON_REQUEST_TOO_LARGE", errorOf(t, rec).Reason)
}
