// Package events provides a typed Kafka event dispatcher and shared helpers
// for envelope headers, topic naming, and the handler signals that steer a
// failed message: poison pill and leave uncommitted.
package events

import "errors"

// ErrPoisonPill marks an error as non-retryable. Handlers that return an error
// wrapping ErrPoisonPill cause the dispatcher to route the message straight to
// the DLQ without consuming retry budget. Example:
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
// no handler failure, and the error reaches Deduplicator.Process, so the
// event_id claim rolls back with it. Run returns at once with an error wrapping
// ErrLeaveUncommitted and never fetches again (see Run for why the stop is
// mandatory and how callers treat it). Kafka redelivers the message to whichever
// consumer owns its partition after this reader is closed.
//
// With WithDedup(outbox.NewDeduplicator(pool)), an outage already under way
// when a message arrives fails pool.Begin before the handler runs, and the
// message is routed as an ordinary retryable failure. So a handler meant to
// pause on a Postgres outage does without WithDedup and calls
// Deduplicator.Process itself, where it can pause on that failure, with the
// event_id from envelope.HeadersFromContext.
//
// It outranks every other classification, ErrPoisonPill included: leaving the
// message loses nothing, and the redelivery is judged afresh. A panic never
// leaves a message uncommitted; it is dead-lettered as handler_panic.
var ErrLeaveUncommitted = errors.New("events: leave the message uncommitted")
