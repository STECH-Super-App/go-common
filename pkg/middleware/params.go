package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

// ParseUUIDParam reads an Echo path parameter and validates it with
// input.UUID: only the 36-character hyphenated form (any case) is accepted —
// braces, "urn:uuid:" and bare 32-hex are refused — and the returned id is
// lower-cased.
//
// On failure it returns a 400 *AppError carrying the supplied reason, a
// message derived from the param name, and the *input.Failure as its cause.
// The reason should follow the `<SERVICE>_<DOMAIN>_<CONDITION>`
// screaming-snake convention (e.g. "TENANT_ID_INVALID"). For
// middleware/internal call sites that do not map to a specific service, use
// input.ReasonUUIDInvalid (COMMON_UUID_INVALID).
//
// Validating at the handler boundary stops malformed UUIDs before they reach
// the repository layer where Postgres would reject them with SQLSTATE 22P02
// and leak as a 500.
func ParseUUIDParam(c echo.Context, paramName, reason string) (string, error) {
	id, f := input.UUID(c.Param(paramName))
	if f != nil {
		return "", commonErrors.New(http.StatusBadRequest).
			Reason(reason).
			Message("invalid " + paramName).
			Cause(f).
			Build()
	}
	return id, nil
}
