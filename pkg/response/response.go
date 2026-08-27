// Package response provides standard HTTP response helpers for STECH services.
//
// Error returned to clients carries a stable machine-readable Reason, optional
// Params for variable data the frontend interpolates into a localized string,
// and optional Details for field-level validation. Message is an English-only
// log/dev fallback; production clients are expected to render localized
// strings from Reason + Params.
package response

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

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

// Accepted writes HTTP 202 with the standard envelope. Use it when a request
// was recorded for later processing rather than applied — e.g. a change request
// filed against an already-approved organisation, where a 200 would falsely
// imply the resource now holds what was sent.
func Accepted(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusAccepted, Response{Success: true, Data: data}) //nolint:forbidigo
}

// JSONError writes the given error as a JSON Response. If err is an *AppError,
// Reason, Params, and Details are forwarded into the wire response. If err is
// any other error, a generic 500 envelope is sent.
//
// The diagnostic log line is written through the request-scoped logger (see
// logError), so it carries service, request_id, and trace_id/span_id and is
// findable by request id and joinable to its trace — and its level follows the
// HTTP status class (5xx→Error, 4xx→Warn) so a routine 401/404 does not inflate
// the error-rate sparkline.
//
// When LOG_EMPTY_REASON_WARN=true, a warn log is emitted whenever an *AppError
// with empty Reason is serialized. Used during the migration sweep to catch
// throw sites that haven't been updated yet.
func JSONError(c echo.Context, err error) error {
	out := &Error{Code: http.StatusInternalServerError, Message: "internal server error"}

	var appErr *commonErrors.AppError
	if errors.As(err, &appErr) {
		out.Code = appErr.Code
		out.Reason = appErr.Reason
		out.Message = appErr.Message
		out.Params = appErr.Params
		out.Details = appErr.Details

		if appErr.Reason == "" && os.Getenv("LOG_EMPTY_REASON_WARN") == "true" {
			logger.FromContext(c.Request().Context()).Warn("AppError serialized with empty Reason",
				zap.Int("code", appErr.Code),
				zap.String("message", appErr.Message),
			)
		}
	}

	logError(c.Request().Context(), out.Code, out.Reason, err)

	return c.JSON(out.Code, Response{Success: false, Error: out}) //nolint:forbidigo
}

// logError writes the error-envelope's diagnostic line through the
// request-scoped logger stored on ctx by middleware.RequestLogger, so the line
// that says WHAT went wrong carries service, request_id, and trace_id/span_id
// and can be found by request id and joined to its trace. logger.FromContext
// never returns nil: with no request logger present it falls back to a logger
// still carrying the service identity, and to a no-op when logging was never
// configured.
//
// The level is derived from the HTTP status class: a 5xx is a server fault
// worth an Error, a 4xx is a client error worth a Warn (seen but not paged, and
// kept off the {level="error"} error-rate sparkline). Anything else (a 2xx/3xx
// reached here defensively) is not logged. The free-text error is preserved in
// the "error" field and the typed AppError Reason, when present, in "reason".
func logError(ctx context.Context, code int, reason string, err error) {
	fields := []zap.Field{
		zap.Int("status", code),
		zap.Error(err),
	}
	if reason != "" {
		fields = append(fields, zap.String("reason", reason))
	}

	log := logger.FromContext(ctx)
	switch {
	case code >= http.StatusInternalServerError:
		log.Error("request failed", fields...)
	case code >= http.StatusBadRequest:
		log.Warn("request rejected", fields...)
	}
}
