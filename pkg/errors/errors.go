// Package errors provides a typed application-error envelope used across STECH services.
//
// AppError carries a stable machine-readable Reason, optional Params for
// variable data the frontend interpolates into a localized string, and
// optional Details for field-level validation errors. Message is an English
// log/dev fallback; clients are expected to localize from Reason + Params.
package errors

import "fmt"

// AppError is the canonical service error envelope.
type AppError struct {
	Code    int
	Reason  string
	Message string
	Params  map[string]any
	Details []FieldError
	// Err is the internal cause for errors.Is/Unwrap chain walking.
	// It is never serialized.
	Err error
}

// Error returns a developer-readable string. Message is the English fallback.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap exposes the wrapped cause for errors.Is and errors.As.
func (e *AppError) Unwrap() error {
	return e.Err
}
