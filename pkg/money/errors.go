package money

import (
	"fmt"
	"net/http"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Reason codes are the canonical i18n keys the frontend renders from
// (Critical Rule 7 — Typed errors only). SCREAMING_SNAKE_CASE.
const (
	ReasonInvalidCurrencyCode = "COMMON_MONEY_INVALID_CURRENCY"
	ReasonCurrencyMismatch    = "COMMON_MONEY_CURRENCY_MISMATCH"
	ReasonOverflow            = "COMMON_MONEY_OVERFLOW"
)

func errInvalidCode(s string) error {
	return commonerr.New(http.StatusUnprocessableEntity).
		Reason(ReasonInvalidCurrencyCode).
		Message(fmt.Sprintf("invalid ISO 4217 currency code %q", s)).
		Params(map[string]any{"code": s}).
		Build()
}

func errMismatch(a, b Code) error {
	return commonerr.New(http.StatusUnprocessableEntity).
		Reason(ReasonCurrencyMismatch).
		Message(fmt.Sprintf("currency mismatch: %s vs %s", a, b)).
		Params(map[string]any{"left": string(a), "right": string(b)}).
		Build()
}

func errOverflow(op string) error {
	return commonerr.New(http.StatusUnprocessableEntity).
		Reason(ReasonOverflow).
		Message("money arithmetic overflow in " + op).
		Params(map[string]any{"op": op}).
		Build()
}
