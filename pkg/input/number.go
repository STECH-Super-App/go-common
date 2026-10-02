package input

import (
	"errors"
	"math"
	"strconv"
)

// Int32 checks v against [lo, hi] and returns it as int32. The int32 return
// type means a value that fits the column type is the only thing a caller can
// bind, so an int4 overflow is a validation failure here, not a pgx encode
// error later. JSON bodies decode into int64 (or json.Number) first and pass
// through here. Out of range → COMMON_NUMBER_OUT_OF_RANGE, params {min, max}.
// lo > hi panics.
func Int32(v int64, lo, hi int32) (int32, *Failure) {
	if lo > hi {
		panic("input: Int32 lo > hi")
	}
	if v < int64(lo) || v > int64(hi) {
		return 0, fail(ReasonNumberOutOfRange, map[string]any{"min": lo, "max": hi})
	}
	return int32(v), nil //nolint:gosec // G115: v is within [lo, hi], both int32
}

// Int64 checks v against [lo, hi]. Out of range →
// COMMON_NUMBER_OUT_OF_RANGE, params {min, max}. lo > hi panics.
func Int64(v int64, lo, hi int64) (int64, *Failure) {
	if lo > hi {
		panic("input: Int64 lo > hi")
	}
	if v < lo || v > hi {
		return 0, fail(ReasonNumberOutOfRange, map[string]any{"min": lo, "max": hi})
	}
	return v, nil
}

// ParseInt32 parses a plain decimal integer (an optional leading '-', then
// digits; no '+', no spaces, no exponent, no underscores) and checks it
// against [lo, hi]. Unparseable → COMMON_NUMBER_INVALID; a well-formed
// number outside the range (including outside int64) →
// COMMON_NUMBER_OUT_OF_RANGE, params {min, max}.
func ParseInt32(raw string, lo, hi int32) (int32, *Failure) {
	if lo > hi {
		panic("input: ParseInt32 lo > hi")
	}
	v, f := parseDecimal(raw, int64(lo), int64(hi))
	if f != nil {
		return 0, f
	}
	return Int32(v, lo, hi)
}

// ParseInt64 is ParseInt32 for int64.
func ParseInt64(raw string, lo, hi int64) (int64, *Failure) {
	if lo > hi {
		panic("input: ParseInt64 lo > hi")
	}
	v, f := parseDecimal(raw, lo, hi)
	if f != nil {
		return 0, f
	}
	return Int64(v, lo, hi)
}

func parseDecimal(raw string, lo, hi int64) (int64, *Failure) {
	digits := raw
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if digits == "" {
		return 0, fail(ReasonNumberInvalid, nil)
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, fail(ReasonNumberInvalid, nil)
		}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// The grammar above leaves ErrRange as the only possible error: the
		// number is well-formed but beyond int64, so it is out of range.
		if errors.Is(err, strconv.ErrRange) {
			return 0, fail(ReasonNumberOutOfRange, map[string]any{"min": lo, "max": hi})
		}
		return 0, fail(ReasonNumberInvalid, nil)
	}
	return v, nil
}

// Page is a validated page request. Number and Size are 1-based / positive;
// Offset = (Number-1) * Size.
type Page struct {
	Number int32
	Size   int32
	Offset int64
}

// ParsePage parses a page number and size from raw query values. An empty
// rawNumber means 1; an empty rawSize means defaultSize. Number must be in
// [1, MaxInt32], Size in [1, maxSize]. A failure reports which input failed
// through Failure.AtPage. Offset is computed in int64; with both factors
// bounded by MaxInt32 the product cannot overflow ((2^31-1)^2 < 2^63), so the
// type itself is the overflow check. Panics unless 1 <= defaultSize <= maxSize.
func ParsePage(rawNumber, rawSize string, defaultSize, maxSize int32) (Page, *Failure) {
	if maxSize < 1 || defaultSize < 1 || defaultSize > maxSize {
		panic("input: ParsePage needs 1 <= defaultSize <= maxSize")
	}

	number := int32(1)
	if rawNumber != "" {
		n, f := ParseInt32(rawNumber, 1, math.MaxInt32)
		if f != nil {
			f.page = pageNumber
			return Page{}, f
		}
		number = n
	}

	size := defaultSize
	if rawSize != "" {
		s, f := ParseInt32(rawSize, 1, maxSize)
		if f != nil {
			f.page = pageSize
			return Page{}, f
		}
		size = s
	}

	return Page{
		Number: number,
		Size:   size,
		Offset: int64(number-1) * int64(size),
	}, nil
}

// MaxItems refuses a collection of n items when n > maxItems →
// COMMON_TOO_MANY_ITEMS, params {max}. maxItems <= 0 panics.
func MaxItems(n, maxItems int) *Failure {
	if maxItems <= 0 {
		panic("input: MaxItems max must be positive")
	}
	if n > maxItems {
		return fail(ReasonTooManyItems, map[string]any{"max": maxItems})
	}
	return nil
}
