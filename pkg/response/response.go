package response

import (
	"errors"
	"net/http"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/labstack/echo/v4"
)

// Response is the standardized API response structure
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	Meta    interface{} `json:"meta,omitempty"`
}

// Error represents a standardized error structure
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Success sends a success response with status 200 OK
func Success(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
	})
}

// Created sends a success response with status 201 Created
func Created(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    data,
	})
}

// Error sends an error response based on the error type
func JSONError(c echo.Context, err error) error {
	var appErr *commonErrors.AppError
	code := http.StatusInternalServerError
	msg := "internal server error"
	var details interface{}
	var errCode int

	if errors.As(err, &appErr) {
		code = appErr.Code
		msg = appErr.Message
		errCode = appErr.Code
	} else {
		errCode = code
	}

	return c.JSON(code, Response{
		Success: false,
		Error: &Error{
			Code:    errCode, // Or a specific internal error code if AppError supports it
			Message: msg,
			Details: details,
		},
	})
}
