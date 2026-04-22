// Package events provides a typed Kafka event dispatcher and shared helpers
// for envelope headers, topic naming, and poison-pill failure signaling.
package events

import "errors"

// ErrPoisonPill marks an error as non-retryable. Handlers that return an error
// wrapping ErrPoisonPill cause the dispatcher to route the message straight to
// the DLQ without consuming retry budget. Example:
//
//	return fmt.Errorf("unknown tenant %s: %w", id, events.ErrPoisonPill)
var ErrPoisonPill = errors.New("events: non-retryable")
