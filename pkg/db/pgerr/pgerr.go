// Package pgerr is the repository-level backstop for input that reached
// Postgres without passing a pkg/input validator.
//
// Each repository's mapPgError calls InvalidInput first. A hit means a
// validator is missing somewhere upstream: the request still gets a 4xx-shaped
// Failure instead of a 500, the stech_input_backstop_total counter moves (and
// pages a ticket), and the Failure's cause names the table and column for the
// service's error log.
//
// Spec: dev-setup docs/superpowers/specs/2026-10-02-input-validation-standard-design.md §5.
package pgerr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/STECH-Super-App/go-common/pkg/input"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// SQLSTATE codes the backstop recognises, plus the label used for pgx's
// client-side integer encode failure.
const (
	SQLStateInvalidByteSequence       = "22021" // invalid_byte_sequence_for_encoding (NUL / bad UTF-8 in text)
	SQLStateUntranslatableCharacter   = "22P05" // untranslatable_character (NUL in jsonb)
	SQLStateStringDataRightTruncation = "22001" // string_data_right_truncation (varchar(n) overflow)
	SQLStateInvalidTextRepresentation = "22P02" // invalid_text_representation (bad uuid/int literal)
	LabelEncode                       = "encode"
)

var backstopTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "stech_input_backstop_total",
	Help: "Inputs rejected by Postgres or by pgx encoding because no validator ran first; any increase is a missing pkg/input call.",
}, []string{"sqlstate"})

func init() {
	metrics.Registry.MustRegister(backstopTotal)
	// Pre-create every series at 0 so increase() has a baseline from the
	// first scrape and the very first backstop hit is visible to the alert.
	for _, code := range []string{
		SQLStateInvalidByteSequence,
		SQLStateUntranslatableCharacter,
		SQLStateStringDataRightTruncation,
		SQLStateInvalidTextRepresentation,
		LabelEncode,
	} {
		backstopTotal.WithLabelValues(code)
	}
}

// InvalidInput reports whether err is Postgres (or pgx) refusing an input
// value, and if so returns the COMMON_INPUT_REJECTED_BY_STORAGE Failure and
// increments stech_input_backstop_total{sqlstate}. It returns (nil, false) for
// nil and for every other error, so it is safe as the first line of any
// mapPgError.
//
// Recognised: *pgconn.PgError with SQLSTATE 22021, 22P05, 22001 or 22P02, and
// pgx v5's integer out-of-range encode error (see isIntEncodeError).
func InvalidInput(err error) (*input.Failure, bool) {
	if err == nil {
		return nil, false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case SQLStateInvalidByteSequence, SQLStateUntranslatableCharacter,
			SQLStateStringDataRightTruncation, SQLStateInvalidTextRepresentation:
			backstopTotal.WithLabelValues(pgErr.Code).Inc()
			return input.RejectedByStorage(fmt.Errorf("sqlstate %s table=%q column=%q: %w",
				pgErr.Code, pgErr.TableName, pgErr.ColumnName, err)), true
		}
		return nil, false
	}

	if isIntEncodeError(err) {
		backstopTotal.WithLabelValues(LabelEncode).Inc()
		return input.RejectedByStorage(fmt.Errorf("pgx encode: %w", err)), true
	}
	return nil, false
}

// isIntEncodeError matches pgx v5's client-side failure to encode an integer
// argument that does not fit the parameter's column type (e.g. int64
// 2147483648 bound to int4). pgx builds it from plain fmt.Errorf values with
// no exported type, so the match is on the message, pinned by
// TestInvalidInput_RealPgxEncodeError against the go.mod pgx version:
//
//	failed to encode args[0]: unable to encode 2147483648 into binary format
//	for int4 (OID 23): 2147483648 is greater than maximum value for int4
//
// Requiring "unable to encode" keeps scan-side (decode) range errors, which
// use the same tail wording, out of the backstop.
func isIntEncodeError(err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "unable to encode ") {
		return false
	}
	return strings.Contains(msg, " is greater than maximum value for ") ||
		strings.Contains(msg, " is less than minimum value for ")
}
