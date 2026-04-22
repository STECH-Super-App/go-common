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
