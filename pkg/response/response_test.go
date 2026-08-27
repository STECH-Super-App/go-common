package response_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSuccess(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	data := map[string]string{"foo": "bar"}
	err := response.Success(c, data)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var res response.Response
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.True(t, res.Success)

	// Convert data to map to compare
	resData, ok := res.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "bar", resData["foo"])
}

func TestCreated(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	data := map[string]string{"id": "123"}
	err := response.Created(c, data)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var res response.Response
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.True(t, res.Success)
}

func TestAccepted_Writes202(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPut, "/", nil), rec)

	err := response.Accepted(c, map[string]string{"id": "abc"})

	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Contains(t, rec.Body.String(), `"abc"`)
}

func TestJSONError_AppError(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	appErr := commonErrors.New(http.StatusBadRequest).
		Message("invalid input").
		Cause(errors.New("inner error")).
		Build()
	err := response.JSONError(c, appErr)

	assert.NoError(t, err) // JSONError returns nil (handled error)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var res response.Response
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.False(t, res.Success)
	assert.NotNil(t, res.Error)
	assert.Equal(t, http.StatusBadRequest, res.Error.Code)
	assert.Equal(t, "invalid input", res.Error.Message)
}

func TestJSONError_IncludesReason(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	appErr := commonErrors.New(http.StatusBadRequest).
		Reason("BAD_INPUT").
		Message("bad input").
		Build()
	err := response.JSONError(c, appErr)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var res response.Response
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.NotNil(t, res.Error)
	assert.Equal(t, "BAD_INPUT", res.Error.Reason)
	assert.Equal(t, http.StatusBadRequest, res.Error.Code)
	assert.Equal(t, "bad input", res.Error.Message)
}

func TestJSONError_StandardError(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	stdErr := errors.New("unknown error")
	err := response.JSONError(c, stdErr)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var res response.Response
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.False(t, res.Success)
	assert.NotNil(t, res.Error)
	assert.Equal(t, http.StatusInternalServerError, res.Error.Code)
	// We might want to mask internal errors, checking implementation
	// Current impl sets msg to "internal server error"
	assert.Equal(t, "internal server error", res.Error.Message)
}

func TestJSONErrorForwardsReasonParamsDetails(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	appErr := commonErrors.New(http.StatusBadRequest).
		Reason("TENANT_VALIDATION_FAILED").
		Message("validation failed").
		Params(map[string]any{"request_id": "req-123"}).
		Details([]commonErrors.FieldError{
			{Field: "inn", Reason: "TENANT_INN_INVALID_LENGTH", Message: "invalid length", Params: map[string]any{"expected": float64(12)}},
		}).
		Build()

	if err := response.JSONError(c, appErr); err != nil {
		t.Fatalf("JSONError returned error: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}

	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    int                       `json:"code"`
			Reason  string                    `json:"reason"`
			Message string                    `json:"message"`
			Params  map[string]any            `json:"params"`
			Details []commonErrors.FieldError `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Success {
		t.Errorf("success: want false, got true")
	}
	if body.Error.Reason != "TENANT_VALIDATION_FAILED" {
		t.Errorf("reason: want TENANT_VALIDATION_FAILED, got %q", body.Error.Reason)
	}
	if body.Error.Params["request_id"] != "req-123" {
		t.Errorf("params.request_id: want req-123, got %v", body.Error.Params["request_id"])
	}
	if len(body.Error.Details) != 1 {
		t.Fatalf("details: want 1 entry, got %d", len(body.Error.Details))
	}
	if body.Error.Details[0].Field != "inn" {
		t.Errorf("details[0].field: want inn, got %q", body.Error.Details[0].Field)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("content-type: want application/json, got %q", rec.Header().Get("Content-Type"))
	}
}

// observedResponseLogger returns a logger writing into an in-memory observer at
// Info level (so it captures both Warn and Error lines).
func observedResponseLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.InfoLevel)
	return zap.New(core), logs
}

// reqWithContextLogger builds an echo.Context whose request carries a
// request-scoped logger holding the exact correlation fields
// middleware.RequestLogger stitches on (service, request_id, and the trace pair
// when a span is active). That is what JSONError's logError reads via
// logger.FromContext.
func reqWithContextLogger(base *zap.Logger) (echo.Context, *httptest.ResponseRecorder) {
	scoped := base.With(
		zap.String("service", "test-service"),
		zap.String("request_id", "req-xyz"),
		zap.String("trace_id", "4bf92f3577b34da6a3ce929d0e0e4736"),
		zap.String("span_id", "00f067aa0ba902b7"),
	)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(logger.IntoContext(req.Context(), scoped))
	rec := httptest.NewRecorder()
	return echo.New().NewContext(req, rec), rec
}

// A routine 4xx logs at WARN carrying the correlation fields + the typed
// Reason, and emits NO ERROR line (which would inflate the error-rate panel).
func TestJSONError_4xxLogsWarnWithCorrelation(t *testing.T) {
	base, logs := observedResponseLogger()
	c, rec := reqWithContextLogger(base)

	appErr := commonErrors.New(http.StatusUnauthorized).
		Reason("COMMON_AUTH_REQUIRED").
		Message("authentication required").
		Build()

	require.NoError(t, response.JSONError(c, appErr))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1, "a 4xx must emit exactly one WARN line")
	f := warns[0].ContextMap()
	assert.Equal(t, "test-service", f["service"])
	assert.Equal(t, "req-xyz", f["request_id"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", f["trace_id"])
	assert.Equal(t, "00f067aa0ba902b7", f["span_id"])
	assert.Equal(t, "COMMON_AUTH_REQUIRED", f["reason"])
	assert.EqualValues(t, http.StatusUnauthorized, f["status"])

	assert.Empty(t, logs.FilterLevelExact(zapcore.ErrorLevel).All(),
		"a routine 4xx must NOT be logged at ERROR")
}

// A 5xx logs at ERROR with the same correlation fields; the free-text error
// (including the wrapped cause) is preserved in the structured "error" field.
func TestJSONError_5xxLogsErrorWithCorrelation(t *testing.T) {
	base, logs := observedResponseLogger()
	c, rec := reqWithContextLogger(base)

	appErr := commonErrors.New(http.StatusInternalServerError).
		Reason("ORDER_INTERNAL").
		Message("db unreachable").
		Cause(errors.New("connection refused")).
		Build()

	require.NoError(t, response.JSONError(c, appErr))
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	errs := logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.Len(t, errs, 1, "a 5xx must emit exactly one ERROR line")
	f := errs[0].ContextMap()
	assert.Equal(t, "test-service", f["service"])
	assert.Equal(t, "req-xyz", f["request_id"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", f["trace_id"])
	assert.Equal(t, "ORDER_INTERNAL", f["reason"])
	assert.EqualValues(t, http.StatusInternalServerError, f["status"])
	assert.Contains(t, f["error"], "db unreachable")
	assert.Contains(t, f["error"], "connection refused")

	assert.Empty(t, logs.FilterLevelExact(zapcore.WarnLevel).All(),
		"a 5xx must not also emit a WARN line")
}

// A non-AppError falls to the generic 500 and is logged at ERROR through the
// context logger with correlation intact but no reason field.
func TestJSONError_StandardErrorLogsErrorWithCorrelation(t *testing.T) {
	base, logs := observedResponseLogger()
	c, rec := reqWithContextLogger(base)

	require.NoError(t, response.JSONError(c, errors.New("boom")))
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	errs := logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.Len(t, errs, 1)
	f := errs[0].ContextMap()
	assert.Equal(t, "test-service", f["service"])
	assert.Equal(t, "req-xyz", f["request_id"])
	assert.NotContains(t, f, "reason", "a bare error carries no typed Reason")
	assert.Contains(t, f["error"], "boom")
}

// The LOG_EMPTY_REASON_WARN diagnostic also rides the request-scoped logger, so
// it carries service + request_id like the main error line (the gap this fix
// closes, applied to the sibling site too).
func TestJSONError_EmptyReasonDiagnosticIsCorrelated(t *testing.T) {
	t.Setenv("LOG_EMPTY_REASON_WARN", "true")

	base, logs := observedResponseLogger()
	c, _ := reqWithContextLogger(base)

	// AppError with an EMPTY Reason at a 4xx.
	appErr := commonErrors.New(http.StatusBadRequest).Message("no reason set").Build()
	require.NoError(t, response.JSONError(c, appErr))

	diag := logs.FilterMessage("AppError serialized with empty Reason").All()
	require.Len(t, diag, 1, "the empty-reason diagnostic must be emitted once")
	f := diag[0].ContextMap()
	assert.Equal(t, zapcore.WarnLevel, diag[0].Level)
	assert.Equal(t, "test-service", f["service"])
	assert.Equal(t, "req-xyz", f["request_id"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", f["trace_id"])
	assert.EqualValues(t, http.StatusBadRequest, f["code"])
	assert.Equal(t, "no reason set", f["message"])
}

// With no request-scoped logger on the context, logError must fall back safely
// (never nil, never panic) and the HTTP response is still written normally.
func TestJSONError_NoContextLoggerDoesNotPanic(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	appErr := commonErrors.New(http.StatusNotFound).
		Reason("X_NOT_FOUND").
		Message("nope").
		Build()

	require.NoError(t, response.JSONError(c, appErr))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
