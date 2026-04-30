package errors

// New starts building an AppError with the given HTTP status code.
// Callers chain Reason, Message, Params, Details, and Cause as needed,
// then call Build to obtain the final *AppError.
func New(code int) *Builder {
	return &Builder{appErr: &AppError{Code: code}}
}

// Builder accumulates fields for an AppError. Methods return the same
// builder for chaining; Build returns the assembled *AppError.
type Builder struct {
	appErr *AppError
}

// Reason sets the stable machine-readable reason code.
// Required for every error that reaches a client.
func (b *Builder) Reason(reason string) *Builder {
	b.appErr.Reason = reason
	return b
}

// Message sets the English log/dev fallback string.
// Must remain English; never localize.
func (b *Builder) Message(message string) *Builder {
	b.appErr.Message = message
	return b
}

// Params sets the variable data the frontend interpolates
// into the localized string for Reason.
func (b *Builder) Params(params map[string]any) *Builder {
	b.appErr.Params = params
	return b
}

// Details sets the field-level validation breakdown.
// Pass when more than one field failed validation in a single request.
func (b *Builder) Details(details []FieldError) *Builder {
	b.appErr.Details = details
	return b
}

// Cause wraps the underlying Go error for errors.Is/Unwrap walking.
// The cause is never serialized into the wire response.
func (b *Builder) Cause(err error) *Builder {
	b.appErr.Err = err
	return b
}

// Build returns the assembled *AppError. After Build the builder must
// not be reused; create a new one with New.
func (b *Builder) Build() *AppError {
	return b.appErr
}
