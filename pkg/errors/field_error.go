package errors

// FieldError describes a single field-level validation failure.
// Used as the element type of AppError.Details.
type FieldError struct {
	Field   string         `json:"field"`
	Reason  string         `json:"reason"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}
