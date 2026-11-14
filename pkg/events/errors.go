// Package events provides a typed Kafka event dispatcher and shared helpers
// for envelope headers, topic naming, and the handler signals that steer a
// failed message: poison pill and leave uncommitted.
package events

import (
	"errors"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

// ErrPoisonPill marks an error as non-retryable. Handlers that return an error
// wrapping ErrPoisonPill cause the dispatcher to route the message straight to
// the DLQ without any retry. Example:
//
//	return fmt.Errorf("unknown tenant %s: %w", id, events.ErrPoisonPill)
var ErrPoisonPill = errors.New("events: non-retryable")

// ErrLeaveUncommitted stops the dispatcher WITHOUT consuming the message in
// hand. A handler returns an error wrapping it when it must stop and the
// message must be neither committed nor forwarded — typically it paused on an
// outage of its own store (Postgres down) and the process is shutting down.
// Handlers run on a context without Run's cancellation (Run detaches it so a
// shutdown finishes the message in flight), so such a handler needs its own
// shutdown signal, e.g. pkg/lifecycle's App.Stopping:
//
//	select {
//	case <-stopping:
//		return fmt.Errorf("postgres unavailable at shutdown: %w", events.ErrLeaveUncommitted)
//	case <-time.After(pause):
//	}
//
// The dispatcher then commits no offset, writes no retry or DLQ copy and counts
// no handler failure, and the error reaches the deduplicator, so the claim
// rolls back with it. Run returns at once with an error wrapping
// ErrLeaveUncommitted and never fetches again (see Run for why the stop is
// mandatory and how callers treat it). Kafka redelivers the message to whichever
// consumer owns its partition after this reader is closed.
//
// Since release 2 most outages need no handler code at all: any error that is
// not a decode failure, an ErrPoisonPill or a validation failure is retried in
// place for about two minutes (see Classify), and an in-place retry that is
// interrupted by a shutdown leaves the message uncommitted by itself.
//
// It outranks every other classification, ErrPoisonPill included: leaving the
// message loses nothing, and the redelivery is judged afresh.
var ErrLeaveUncommitted = errors.New("events: leave the message uncommitted")

// ErrDeadLetterWriteFailed marks the error Run returns when the dead-letter (or
// legacy retry-tier) write of a failed message itself failed. The dispatcher
// stops: it commits nothing and fetches nothing more, so the message is not
// lost; the process must exit non-zero (through pkg/lifecycle, see
// Dispatcher.Worker) and Kafka redelivers the message after the restart.
//
// Until release 2 the dispatcher logged "leaving offset uncommitted" and went
// on to the next message, whose commit then moved the partition's offset past
// the one it had just failed to dead-letter (SL-00, go-common#38).
var ErrDeadLetterWriteFailed = errors.New("events: dead-letter write failed")

// Reasons of the typed errors this package returns (Critical Rule 7).
const (
	// ReasonDeadLetterWriteFailed: the 500 Run returns with ErrDeadLetterWriteFailed
	// in its chain.
	ReasonDeadLetterWriteFailed = "COMMON_EVENTS_DEAD_LETTER_WRITE_FAILED"
	// ReasonDedupFailed: the deduplicator's own transaction failed (begin, claim
	// or commit), never the handler's error.
	ReasonDedupFailed = "COMMON_EVENTS_DEDUP_FAILED"
	// ReasonMessageTimeout: a handler outlived the per-message time limit.
	ReasonMessageTimeout = "COMMON_EVENTS_MESSAGE_TIMEOUT"
)

func internalError(reason, message string, cause error) *commonerrors.AppError {
	return commonerrors.New(500).Reason(reason).Message(message).Cause(cause).Build()
}
