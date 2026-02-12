package response_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

	appErr := commonErrors.BadRequest("invalid input", errors.New("inner error"))
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
