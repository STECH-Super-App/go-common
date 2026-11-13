package response_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
	"github.com/STECH-Super-App/go-common/pkg/response"
)

func renderError(t *testing.T, err error) (int, response.Error) {
	t.Helper()
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)

	require.NotPanics(t, func() { require.NoError(t, response.JSONError(c, err)) })

	var res response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.NotNil(t, res.Error)
	return rec.Code, *res.Error
}

// SL-14: JSONError understood only *AppError, so an *input.Failure — the
// documented return of a domain constructor — became a 500 with no Reason, the
// outcome the validation backstop exists to prevent.
func TestJSONError_InputFailureIs422WithItsReason(t *testing.T) {
	_, fail := input.Line("", 10, input.Required())
	require.NotNil(t, fail)

	code, out := renderError(t, fmt.Errorf("create listing: %w", fail))

	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, input.ReasonTextRequired, out.Reason)
	assert.Equal(t, http.StatusUnprocessableEntity, out.Code)
	assert.NotEmpty(t, out.Message)
}

func TestJSONError_StorageBackstopFailureIs400(t *testing.T) {
	code, out := renderError(t, input.RejectedByStorage(errors.New("sqlstate 22001")))

	assert.Equal(t, http.StatusBadRequest, code, "the storage backstop is a 400, as the gateway's encoding guard")
	assert.Equal(t, input.ReasonInputRejectedByStorage, out.Reason)
	assert.NotContains(t, out.Message, "22001", "the storage cause must never reach a client")
}

// SL-14: *echo.HTTPError from c.Bind (and the router's 404/405) fell to the
// generic 500.
func TestJSONError_EchoHTTPErrorKeepsItsStatus(t *testing.T) {
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{echo.NewHTTPError(http.StatusBadRequest, "Syntax error: offset=3, error=invalid character"), 400, "COMMON_BAD_REQUEST"},
		{echo.ErrNotFound, 404, "COMMON_NOT_FOUND"},
		{echo.ErrMethodNotAllowed, 405, "COMMON_METHOD_NOT_ALLOWED"},
		{echo.ErrStatusRequestEntityTooLarge, 413, "COMMON_REQUEST_TOO_LARGE"},
		{echo.ErrUnsupportedMediaType, 415, "COMMON_UNSUPPORTED_MEDIA_TYPE"},
		{echo.ErrTooManyRequests, 429, "COMMON_TOO_MANY_REQUESTS"},
		{echo.NewHTTPError(http.StatusTeapot, "x"), 418, "COMMON_BAD_REQUEST"},
		{echo.NewHTTPError(http.StatusBadGateway, "x"), 502, "COMMON_INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			code, out := renderError(t, fmt.Errorf("bind: %w", tc.err))
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.reason, out.Reason)
			assert.NotContains(t, out.Message, "offset=", "bind internals must not reach a client")
		})
	}
}

func TestJSONError_GenericFallbackCarriesACommonInternalReason(t *testing.T) {
	code, out := renderError(t, errors.New("boom"))

	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, "COMMON_INTERNAL", out.Reason, "the frontend needs an i18n key even for an unclassified fault")
	assert.Equal(t, "internal server error", out.Message)
}

// SL-14: an AppError with Code == 0 made WriteHeader(0) panic.
func TestJSONError_InvalidStatusCodeIsClampedTo500(t *testing.T) {
	for _, bad := range []int{0, -1, 99, 600, 1000} {
		code, out := renderError(t, commonErrors.New(bad).Reason("X_FAILED").Message("x").Build())

		assert.Equal(t, http.StatusInternalServerError, code, "code %d", bad)
		assert.Equal(t, http.StatusInternalServerError, out.Code)
		assert.Equal(t, "X_FAILED", out.Reason, "the typed Reason is kept; only the status is repaired")
	}
}

// A body cut off by http.MaxBytesReader reaches the handler as a failed read;
// c.Bind wraps it in a 400, but the mistake is the size.
func TestJSONError_MaxBytesErrorIs413EvenWhenBindWrappedItIn400(t *testing.T) {
	bindErr := echo.NewHTTPError(http.StatusBadRequest, "Bad request").SetInternal(&http.MaxBytesError{Limit: 16})

	code, out := renderError(t, bindErr)

	assert.Equal(t, http.StatusRequestEntityTooLarge, code)
	assert.Equal(t, "COMMON_REQUEST_TOO_LARGE", out.Reason)
}
