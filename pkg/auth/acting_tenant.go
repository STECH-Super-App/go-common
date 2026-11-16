package auth

import (
	"net/http"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

const (
	// HeaderActingTenantID names the tenant a caller asks to act as
	// (Messenger). It is a client SELECTOR, never proof of membership: the
	// caller's seats come from the gateway-set X-Tenant-Memberships, and the
	// service checks the selector against them. Not a gateway-asserted header
	// (pkg/trustedheaders does not strip it).
	HeaderActingTenantID = "X-Acting-Tenant-Id"

	// ReasonActingTenantInvalid is the Reason of the 400 ParseActingTenantID returns.
	ReasonActingTenantInvalid = "COMMON_ACTING_TENANT_INVALID"

	zeroUUID = "00000000-0000-0000-0000-000000000000"
)

// ParseActingTenantID validates the selector only; it is never membership proof.
func ParseActingTenantID(raw string) (string, error) {
	id, e := input.UUID(raw)
	if e != nil || id == zeroUUID {
		return "", commonerr.New(http.StatusBadRequest).Reason(ReasonActingTenantInvalid).Message("Invalid acting tenant").Build()
	}
	return id, nil
}
