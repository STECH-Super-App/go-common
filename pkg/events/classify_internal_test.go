package events

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A payload that does not decode is dead-lettered at once: the same bytes will
// not decode on the next attempt either. errUnmarshal is what the registered
// handlers return for it.
func TestClassify_DecodeFailure(t *testing.T) {
	err := fmt.Errorf("%w: invalid character", errUnmarshal)
	assert.Equal(t, ClassDecode, Classify(err))
	assert.True(t, Classify(err).DeadLetterNow())
	assert.Equal(t, reasonUnmarshal, reasonOf(ClassDecode))
}

// A recovered panic is its own class and dead-letters at once, whatever its
// value is or wraps (owner decision 14.11.2026).
func TestClassify_PanicIsDeadLetteredAtOnce(t *testing.T) {
	for name, value := range map[string]any{
		"a string":                  "nil pointer",
		"an error":                  fmt.Errorf("db down"),
		"an error wrapping a pill":  fmt.Errorf("x: %w", ErrPoisonPill),
		"the leave-uncommitted one": ErrLeaveUncommitted,
		"nil":                       nil,
	} {
		t.Run(name, func(t *testing.T) {
			err := newPanicError(value)
			assert.Equal(t, ClassPanic, Classify(err))
			assert.Equal(t, ClassPanic, Classify(fmt.Errorf("apply: %w", err)), "wrapped")
			assert.True(t, Classify(err).DeadLetterNow())
			assert.Equal(t, reasonHandlerPanic, reasonOf(ClassPanic))
			assert.NotEmpty(t, err.stack, "the stack is kept for the log")
		})
	}
	assert.Equal(t, reasonMaxRetries, reasonOf(ClassRetryable))
}

func TestRetryDelay_DoublesCapsAndJitters(t *testing.T) {
	d := &Dispatcher{retryInitial: 500 * time.Millisecond, retryMax: 15 * time.Second}
	nominal := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second}
	for attempt, want := range nominal {
		for i := 0; i < 50; i++ {
			got := d.retryDelay(attempt + 1)
			assert.GreaterOrEqual(t, got, time.Duration(float64(want)*0.8), "attempt %d", attempt+1)
			assert.LessOrEqual(t, got, time.Duration(float64(want)*1.2), "attempt %d", attempt+1)
		}
	}
}

func TestDispatcherDefaults(t *testing.T) {
	d := NewDispatcher(nil, nil, NewMemoryDeduplicator(), WithGroup("g"))
	assert.Equal(t, 2*time.Minute, d.retryWindow, "about two minutes in place before a dead letter")
	assert.Equal(t, time.Minute, d.messageTimeout)
	assert.Equal(t, 500*time.Millisecond, d.retryInitial)
	assert.Equal(t, 15*time.Second, d.retryMax)
}

func TestDispatcherDefaultsFromEnvironment(t *testing.T) {
	t.Setenv("EVENTS_RETRY_WINDOW", "7s")
	t.Setenv("EVENTS_MESSAGE_TIMEOUT", "3s")
	t.Setenv("EVENTS_RETRY_INITIAL_BACKOFF", "20ms")
	t.Setenv("EVENTS_RETRY_MAX_BACKOFF", "90ms")
	d := NewDispatcher(nil, nil, NewMemoryDeduplicator(), WithGroup("g"))
	assert.Equal(t, 7*time.Second, d.retryWindow)
	assert.Equal(t, 3*time.Second, d.messageTimeout)
	assert.Equal(t, 20*time.Millisecond, d.retryInitial)
	assert.Equal(t, 90*time.Millisecond, d.retryMax)

	d = NewDispatcher(nil, nil, NewMemoryDeduplicator(), WithGroup("g"), WithRetryWindow(time.Second), WithMessageTimeout(time.Second))
	assert.Equal(t, time.Second, d.retryWindow, "an option beats the environment")
	assert.Equal(t, time.Second, d.messageTimeout)
}
