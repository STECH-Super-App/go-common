package middleware

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

// ParseUUIDParam reads an Echo path parameter and validates it as a UUID.
//
// On parse failure it returns a 400 *AppError carrying the supplied reason
// and a message derived from the param name. The reason should follow the
// `<SERVICE>_<DOMAIN>_<CONDITION>` screaming-snake convention (e.g.
// "TENANT_ID_INVALID"). For middleware/internal call sites that do not map
// to a specific service, use the `COMMON_*` prefix.
//
// Validating at the handler boundary stops malformed UUIDs before they reach
// the repository layer where Postgres would reject them with SQLSTATE 22P02
// and leak as a 500. The returned id is the canonical UUID string form.
func ParseUUIDParam(c echo.Context, paramName, reason string) (string, error) {
	id, err := uuid.Parse(c.Param(paramName))
	if err != nil {
		return "", commonErrors.New(http.StatusBadRequest).
			Reason(reason).
			Message("invalid " + paramName).
			Cause(err).
			Build()
	}
	return id.String(), nil
}
