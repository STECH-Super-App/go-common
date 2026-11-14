package events

import (
	"errors"
	"net/http"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

// Class says what the dispatcher does with a failed message.
type Class string

// The classes. The first three are the ONLY ways to be dead-lettered at once;
// everything else is ClassRetryable.
const (
	// ClassDecode: the payload cannot be decoded into the handler's proto
	// message. Retrying the same bytes cannot help.
	ClassDecode Class = "decode"
	// ClassPoisonPill: the handler said so, by wrapping ErrPoisonPill.
	ClassPoisonPill Class = "poison_pill"
	// ClassValidation: the event is invalid. An *errors.AppError with HTTP
	// status 400 or 422, or an *input.Failure anywhere in the chain.
	ClassValidation Class = "validation"
	// ClassRetryable: anything else, whatever it is — a 404 (the parent row
	// is not there YET), a 409, a 503, a timeout, a failed query, a panic, an
	// error nobody classified. Retried in place with backoff until the retry
	// window (two minutes by default) is spent, then dead-lettered with an
	// error log and events_consumer_dead_lettered_total{reason="max_retries"}.
	ClassRetryable Class = "retryable"
)

// DeadLetterNow reports whether a message of this class is dead-lettered on
// the first failure instead of being retried.
func (c Class) DeadLetterNow() bool { return c != ClassRetryable }

// Classify is the platform's ONE rule for what a failed message deserves
// (audit-fix campaign spec §3.1, SL-11). The dispatcher calls it; services call
// it in their own tests to pin what their handlers return, and sale-service's
// PHP consumers follow the same table by hand.
//
//	err                                                   class
//	----------------------------------------------------  --------------
//	the payload did not decode                            ClassDecode
//	wraps ErrPoisonPill                                   ClassPoisonPill
//	*errors.AppError with status 400 or 422, or           ClassValidation
//	  an *input.Failure
//	anything else (404, 409, 5xx, timeouts, unknown)      ClassRetryable
//
// A 404 or 409 is retryable ON PURPOSE: it is usually an ordering race (the
// event arrived before the row it refers to) that the next attempt resolves, and
// dead-lettering it turns every short outage into manual replays. A handler that
// knows a 404 is final marks it with ErrPoisonPill.
//
// ErrLeaveUncommitted outranks the table and is handled before Classify is
// called; nil is ClassRetryable only because it is not a failure — callers do
// not ask.
func Classify(err error) Class {
	switch {
	case errors.Is(err, errUnmarshal):
		return ClassDecode
	case errors.Is(err, ErrPoisonPill):
		return ClassPoisonPill
	case isValidation(err):
		return ClassValidation
	default:
		return ClassRetryable
	}
}

func isValidation(err error) bool {
	var failure *input.Failure
	if errors.As(err, &failure) {
		return true
	}
	var app *commonerrors.AppError
	if errors.As(err, &app) {
		return app.Code == http.StatusBadRequest || app.Code == http.StatusUnprocessableEntity
	}
	return false
}
