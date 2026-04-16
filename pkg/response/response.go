// Package response provides standardized API response structures and helper functions.
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

// Error represents a standardized error structure.
//
// Code is the numeric HTTP status. Reason is a stable, machine-readable
// identifier (e.g. "TRANSFER_EXPIRED") suitable for client-side branching;
// it may be empty on legacy call sites that predate the reason catalog.
type Error struct {
	Code    int         `json:"code"`
	Reason  string      `json:"reason,omitempty"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Success sends a success response with status 200 OK
func Success(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, Response{ //nolint:forbidigo
		Success: true,
		Data:    data,
	})
}

// Created sends a success response with status 201 Created
func Created(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusCreated, Response{ //nolint:forbidigo
		Success: true,
		Data:    data,
	})
}

// JSONError sends an error response based on the error type
func JSONError(c echo.Context, err error) error {
	// Log error to stdout
	c.Logger().Error(err)
	var appErr *commonErrors.AppError
	code := http.StatusInternalServerError
	msg := "internal server error"
	var reason string
	var details interface{}

	if errors.As(err, &appErr) {
		code = appErr.Code
		msg = appErr.Message
		reason = appErr.Reason
	}

	return c.JSON(code, Response{ //nolint:forbidigo
		Success: false,
		Error: &Error{
			Code:    code,
			Reason:  reason,
			Message: msg,
			Details: details,
		},
	})
}
