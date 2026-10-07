package events_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/events"
)

func TestErrPoisonPill_wrapping(t *testing.T) {
	wrapped := fmt.Errorf("bad input: %w", events.ErrPoisonPill)
	if !errors.Is(wrapped, events.ErrPoisonPill) {
		t.Fatalf("errors.Is(wrapped, ErrPoisonPill) = false, want true")
	}
}

func TestErrLeaveUncommitted_wrapping(t *testing.T) {
	wrapped := fmt.Errorf("paused at shutdown: %w", events.ErrLeaveUncommitted)
	if !errors.Is(wrapped, events.ErrLeaveUncommitted) {
		t.Fatalf("errors.Is(wrapped, ErrLeaveUncommitted) = false, want true")
	}
	joined := errors.Join(errors.New("postgres down"), events.ErrLeaveUncommitted)
	if !errors.Is(joined, events.ErrLeaveUncommitted) {
		t.Fatalf("errors.Is(joined, ErrLeaveUncommitted) = false, want true")
	}
	if errors.Is(events.ErrLeaveUncommitted, events.ErrPoisonPill) || errors.Is(events.ErrPoisonPill, events.ErrLeaveUncommitted) {
		t.Fatalf("ErrLeaveUncommitted and ErrPoisonPill must stay distinct")
	}
}
