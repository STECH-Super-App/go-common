// Package response provides standard HTTP response helpers for STECH services.
//
// Error returned to clients carries a stable machine-readable Reason, optional
// Params for variable data the frontend interpolates into a localized string,
// and optional Details for field-level validation. Message is an English-only
// log/dev fallback; production clients are expected to render localized
// strings from Reason + Params.
package response

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// Response is the standard envelope for all STECH HTTP responses.
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	Meta    interface{} `json:"meta,omitempty"`
}

// Error is the on-wire shape of an HTTP error response.
// Reason is the stable, machine-readable key the frontend translates against.
// Params carries variable data the localized string interpolates.
// Message is English-only, log/dev fallback.
// Details is non-nil for validation errors with multiple field failures.
type Error struct {
	Code    int                       `json:"code"`
	Reason  string                    `json:"reason,omitempty"`
	Message string                    `json:"message"`
	Params  map[string]any            `json:"params,omitempty"`
	Details []commonErrors.FieldError `json:"details,omitempty"`
}

// Success writes a 200 OK with the given data.
func Success(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, Response{Success: true, Data: data}) //nolint:forbidigo
}

// Created writes a 201 Created with the given data.
func Created(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusCreated, Response{Success: true, Data: data}) //nolint:forbidigo
}

// JSONError writes the given error as a JSON Response. If err is an *AppError,
// Reason, Params, and Details are forwarded into the wire response. If err is
// any other error, a generic 500 envelope is sent and the error is logged.
//
// When LOG_EMPTY_REASON_WARN=true, a warn log is emitted whenever an *AppError
// with empty Reason is serialized. Used during the migration sweep to catch
// throw sites that haven't been updated yet.
func JSONError(c echo.Context, err error) error {
	c.Logger().Error(err)

	out := &Error{Code: http.StatusInternalServerError, Message: "internal server error"}

	var appErr *commonErrors.AppError
	if errors.As(err, &appErr) {
		out.Code = appErr.Code
		out.Reason = appErr.Reason
		out.Message = appErr.Message
		out.Params = appErr.Params
		out.Details = appErr.Details

		if appErr.Reason == "" && os.Getenv("LOG_EMPTY_REASON_WARN") == "true" {
			logger.Warn("AppError serialized with empty Reason",
				logger.Int("code", appErr.Code),
				logger.String("message", appErr.Message),
			)
		}
	}

	return c.JSON(out.Code, Response{Success: false, Error: out}) //nolint:forbidigo
}
