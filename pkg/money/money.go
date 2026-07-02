// Package money is the platform's sanctioned money primitive: an immutable
// amount in integer minor units paired with a validated currency Code.
// Floats are forbidden for money across STECH; this package is the only
// approved representation. Scope is deliberately minimal (checked Add/Mul/Cmp)
// — no FX, no rounding, no formatting (YAGNI).
package money

import "math"

// Code is a syntactically valid ISO 4217 alphabetic currency code (three
// uppercase ASCII letters). Registry membership is deliberately not checked:
// which currencies a service accepts is that service's policy, not this
// package's (order-service whitelists RUB only).
type Code string

// ParseCode validates that s is exactly three uppercase ASCII letters and
// returns it as a Code, or errInvalidCode otherwise.
func ParseCode(s string) (Code, error) {
	if len(s) != 3 {
		return "", errInvalidCode(s)
	}
	for i := 0; i < 3; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return "", errInvalidCode(s)
		}
	}
	return Code(s), nil
}

// Money is an immutable amount in integer minor units (kopecks for RUB).
// Floats are forbidden across the platform for money; this type is the only
// sanctioned representation. Zero value is invalid — construct via New.
type Money struct {
	amountMinor int64
	currency    Code
}

// New constructs a Money after validating the currency Code, returning the
// zero Money and the validation error on an invalid code.
func New(amountMinor int64, c Code) (Money, error) {
	if _, err := ParseCode(string(c)); err != nil {
		return Money{}, err
	}
	return Money{amountMinor: amountMinor, currency: c}, nil
}

// AmountMinor returns the amount in integer minor units.
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency returns the currency Code.
func (m Money) Currency() Code { return m.currency }

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m.amountMinor == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.amountMinor < 0 }

// Add returns m+o, failing on cross-currency operands or int64 overflow.
// The overflow bound is checked before adding, not after. A sum of exactly
// math.MinInt64 is rejected as overflow: excluding it keeps the representable
// domain symmetric ([MinInt64+1, MaxInt64]) so every valid amount can be
// negated without overflow.
func (m Money) Add(o Money) (Money, error) {
	if m.currency != o.currency {
		return Money{}, errMismatch(m.currency, o.currency)
	}
	if (o.amountMinor > 0 && m.amountMinor > math.MaxInt64-o.amountMinor) ||
		(o.amountMinor < 0 && m.amountMinor <= math.MinInt64-o.amountMinor) {
		return Money{}, errOverflow("add")
	}
	return Money{amountMinor: m.amountMinor + o.amountMinor, currency: m.currency}, nil
}

// Mul returns m×n with overflow checking. A zero operand short-circuits to a
// zero Money in m's currency. The explicit check for math.MinInt64 * -1 is
// required because Go's two's-complement wrap (MinInt64 * -1 == MinInt64) makes
// the p/n != amountMinor check insufficient to catch this case.
func (m Money) Mul(n int64) (Money, error) {
	if m.amountMinor == 0 || n == 0 {
		return Money{amountMinor: 0, currency: m.currency}, nil
	}
	if m.amountMinor == math.MinInt64 && n == -1 {
		return Money{}, errOverflow("mul")
	}
	p := m.amountMinor * n
	if p/n != m.amountMinor {
		return Money{}, errOverflow("mul")
	}
	return Money{amountMinor: p, currency: m.currency}, nil
}

// Cmp returns -1, 0, or 1 as m is less than, equal to, or greater than o.
// Comparing across currencies is an error.
func (m Money) Cmp(o Money) (int, error) {
	if m.currency != o.currency {
		return 0, errMismatch(m.currency, o.currency)
	}
	switch {
	case m.amountMinor < o.amountMinor:
		return -1, nil
	case m.amountMinor > o.amountMinor:
		return 1, nil
	default:
		return 0, nil
	}
}
