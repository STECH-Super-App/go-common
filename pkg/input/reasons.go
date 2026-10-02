package input

// Reason codes emitted by this package (and by the api-gateway encoding guard
// and the pgerr storage backstop). Each is the i18n key the frontend renders
// from; i18n-catalog carries the translated strings.
const (
	ReasonTextRequired          = "COMMON_TEXT_REQUIRED"
	ReasonTextTooLong           = "COMMON_TEXT_TOO_LONG"
	ReasonTextInvalidCharacters = "COMMON_TEXT_INVALID_CHARACTERS"
	ReasonUUIDInvalid           = "COMMON_UUID_INVALID"
	ReasonNumberInvalid         = "COMMON_NUMBER_INVALID"
	ReasonNumberOutOfRange      = "COMMON_NUMBER_OUT_OF_RANGE"
	ReasonTooManyItems          = "COMMON_TOO_MANY_ITEMS"
	ReasonPhoneInvalid          = "COMMON_PHONE_INVALID"
	ReasonEmailInvalid          = "COMMON_EMAIL_INVALID"
	// ReasonInputRejectedByStorage is the pgerr backstop's Reason: a value
	// reached Postgres without passing a validator and Postgres refused it.
	ReasonInputRejectedByStorage = "COMMON_INPUT_REJECTED_BY_STORAGE"
	// ReasonRequestEncodingInvalid is the api-gateway's Reason for a decoded
	// path segment or query key/value that holds NUL or invalid UTF-8.
	ReasonRequestEncodingInvalid = "COMMON_REQUEST_ENCODING_INVALID"
)

// englishMessages is the English fallback for every Reason above. Params are
// not interpolated here; clients localize from Reason + Params.
var englishMessages = map[string]string{
	ReasonTextRequired:           "value is required",
	ReasonTextTooLong:            "value is too long",
	ReasonTextInvalidCharacters:  "value contains invalid characters",
	ReasonUUIDInvalid:            "value is not a valid UUID",
	ReasonNumberInvalid:          "value is not a valid integer",
	ReasonNumberOutOfRange:       "value is out of the allowed range",
	ReasonTooManyItems:           "too many items",
	ReasonPhoneInvalid:           "value is not a valid phone number",
	ReasonEmailInvalid:           "value is not a valid email address",
	ReasonInputRejectedByStorage: "value was rejected by storage",
	ReasonRequestEncodingInvalid: "request path or query contains invalid encoding",
}

// MessageFor returns the English fallback message for a Reason of this
// package, for use as AppError.Message. An unknown reason yields "invalid
// input".
func MessageFor(reason string) string {
	if m, ok := englishMessages[reason]; ok {
		return m
	}
	return "invalid input"
}
