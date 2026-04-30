package response_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/response"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
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
