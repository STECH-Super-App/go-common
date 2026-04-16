// Package errors provides application-specific error types.
package errors //nolint:revive

import (
	"fmt"
	"net/http"
)

// AppError represents an application error with an HTTP status code and a
// stable, machine-readable reason string. Reason is optional — legacy call
// sites may omit it and the envelope will simply leave the field empty.
type AppError struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
	Err     error  `json:"-"` // Internal error, not exposed to users
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap returns the underlying error.
func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError creates a new AppError.
func NewAppError(code int, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// NewAppErrorWithReason creates a new AppError with a stable reason tag.
func NewAppErrorWithReason(code int, message, reason string, err error) *AppError {
	return &AppError{
		Code:    code,
		Reason:  reason,
		Message: message,
		Err:     err,
	}
}

// ---------- Legacy shortcut constructors (no reason) ----------

// BadRequest creates a 400 Bad Request error.
func BadRequest(message string, err error) *AppError {
	return NewAppError(http.StatusBadRequest, message, err)
}

// Unauthorized creates a 401 Unauthorized error.
func Unauthorized(message string, err error) *AppError {
	return NewAppError(http.StatusUnauthorized, message, err)
}

// Forbidden creates a 403 Forbidden error.
func Forbidden(message string, err error) *AppError {
	return NewAppError(http.StatusForbidden, message, err)
}

// NotFound creates a 404 Not Found error.
func NotFound(message string, err error) *AppError {
	return NewAppError(http.StatusNotFound, message, err)
}

// Conflict creates a 409 Conflict error.
func Conflict(message string, err error) *AppError {
	return NewAppError(http.StatusConflict, message, err)
}

// Gone creates a 410 Gone error.
func Gone(message string, err error) *AppError {
	return NewAppError(http.StatusGone, message, err)
}

// InternalServerError creates a 500 Internal Server Error.
func InternalServerError(message string, err error) *AppError {
	return NewAppError(http.StatusInternalServerError, message, err)
}

// ---------- Reason-tagged constructors (preferred for new code) ----------

// BadRequestWithReason returns a 400 with a stable reason tag.
func BadRequestWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusBadRequest, message, reason, err)
}

// UnauthorizedWithReason returns a 401 with a stable reason tag.
func UnauthorizedWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusUnauthorized, message, reason, err)
}

// ForbiddenWithReason returns a 403 with a stable reason tag.
func ForbiddenWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusForbidden, message, reason, err)
}

// NotFoundWithReason returns a 404 with a stable reason tag.
func NotFoundWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusNotFound, message, reason, err)
}

// ConflictWithReason returns a 409 with a stable reason tag.
func ConflictWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusConflict, message, reason, err)
}

// GoneWithReason returns a 410 with a stable reason tag.
func GoneWithReason(message, reason string, err error) *AppError {
	return NewAppErrorWithReason(http.StatusGone, message, reason, err)
}
