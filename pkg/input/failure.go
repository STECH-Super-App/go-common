package input

import (
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Failure is one validation failure. It implements error, so a domain
// constructor may return it as its error and a handler finds it with
// errors.As.
type Failure struct {
	Reason string
	Params map[string]any

	// cause is set only by the storage backstop (RejectedByStorage): it names
	// the SQLSTATE and, when Postgres supplies them, table and column, so the
	// service's error log says which field slipped past validation.
	cause error

	// page marks which input of ParsePage failed; empty for every other
	// validator. Read by AtPage.
	page pagePart
}

type pagePart uint8

const (
	pageNone pagePart = iota
	pageNumber
	pageSize
)

func fail(reason string, params map[string]any) *Failure {
	return &Failure{Reason: reason, Params: params}
}

// RejectedByStorage builds the backstop Failure (Reason
// COMMON_INPUT_REJECTED_BY_STORAGE) around the storage error that caused it.
// The cause is reachable via errors.Unwrap / errors.As and printed by Error,
// but never reaches a client: At copies only Reason, Message and Params.
func RejectedByStorage(cause error) *Failure {
	return &Failure{Reason: ReasonInputRejectedByStorage, cause: cause}
}

// Error returns "<REASON>: <english message>", plus the cause when present.
func (f *Failure) Error() string {
	msg := f.Reason + ": " + MessageFor(f.Reason)
	if f.cause != nil {
		msg += ": " + f.cause.Error()
	}
	return msg
}

// Unwrap exposes the backstop cause (nil for every validator failure).
func (f *Failure) Unwrap() error {
	return f.cause
}

// At attaches the field name and returns the FieldError for AppError.Details.
// It is nil-safe: on a nil *Failure it returns nil, which is what lets a
// handler write input.Collect(a.At("name"), b.At("email")) over results that
// may or may not have failed.
func (f *Failure) At(field string) *commonErrors.FieldError {
	if f == nil {
		return nil
	}
	return &commonErrors.FieldError{
		Field:   field,
		Reason:  f.Reason,
		Message: MessageFor(f.Reason),
		Params:  f.Params,
	}
}

// AtPage is At for a ParsePage failure: it names numberField or sizeField
// depending on which of the two raw inputs failed. On a Failure that did not
// come from ParsePage it falls back to numberField. Nil-safe like At.
func (f *Failure) AtPage(numberField, sizeField string) *commonErrors.FieldError {
	if f == nil {
		return nil
	}
	if f.page == pageSize {
		return f.At(sizeField)
	}
	return f.At(numberField)
}

// Collect drops nil entries and returns the rest in order — nil when every
// entry was nil, so `if details := input.Collect(...); details != nil` reads
// as "something failed".
func Collect(fields ...*commonErrors.FieldError) []commonErrors.FieldError {
	var out []commonErrors.FieldError
	for _, f := range fields {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}
