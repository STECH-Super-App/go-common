package events_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// runOne feeds one message to a dispatcher and returns once it was committed,
// the dispatcher stopped, or a second has passed.
func runOne(t *testing.T, disp *events.Dispatcher, reader *greedyReader) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case err := <-done:
			return err
		case <-deadline:
			cancel()
			t.Fatal("dispatcher neither committed nor stopped in time")
		case <-time.After(2 * time.Millisecond):
			if _, c := reader.snapshot(); len(c) > 0 {
				cancel()
				return <-done
			}
		}
	}
}

// SL-11: a 404, a 409 or a 503 is an outage or a race, not a bad message. On
// the release 1 tree the first one dead-lettered with reason max_retries.
func TestDispatcher_RetryableErrorsAreRetriedInPlaceAndSucceed(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
			dlq := &fakeWriter{}
			var calls atomic.Int32
			disp := newDisp(reader, dlq, events.WithRetryWindow(5*time.Second))
			events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
				if calls.Add(1) < 4 {
					return commonerrors.New(status).Reason("X").Message("not yet").Build()
				}
				return nil
			})

			err := runOne(t, disp, reader)

			require.ErrorIs(t, err, context.Canceled)
			assert.EqualValues(t, 4, calls.Load(), "three failures, then the attempt that works")
			assert.Empty(t, dlq.captured(), "nothing is dead-lettered")
			_, commits := reader.snapshot()
			assert.Len(t, commits, 1, "the message is committed once, after it succeeded")
		})
	}
}

func TestDispatcher_RetriesExhaustedDeadLettersWithMaxRetries(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	dlq := &fakeWriter{}
	var calls atomic.Int32
	disp := newDisp(reader, dlq) // 50 ms window, 5-10 ms backoff
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		calls.Add(1)
		return commonerrors.New(http.StatusServiceUnavailable).Reason("X_UPSTREAM_DOWN").Message("down").Build()
	})

	_ = runOne(t, disp, reader)

	assert.Greater(t, calls.Load(), int32(2), "retried in place more than once")
	dead := dlq.captured()
	require.Len(t, dead, 1)
	assert.Equal(t, "max_retries", string(findTestHeader(dead[0], "x-dlq-reason")))
	assert.Contains(t, string(findTestHeader(dead[0], "x-dlq-error")), "down")
	_, commits := reader.snapshot()
	assert.Len(t, commits, 1, "committed only after the dead letter was written")
}

// Dead-lettered at once: no retry at all.
func TestDispatcher_DeadLettersAtOnceOnlyForDecodePoisonAndValidation(t *testing.T) {
	_, inputFailure := inputRequired()
	cases := []struct {
		name   string
		err    error
		raw    []byte // a payload that does not decode, instead of a handler error
		reason string
	}{
		{"validation 422", commonerrors.New(http.StatusUnprocessableEntity).Reason("X_INVALID").Message("bad").Build(), nil, "validation"},
		{"validation 400", commonerrors.New(http.StatusBadRequest).Reason("X_INVALID").Message("bad").Build(), nil, "validation"},
		{"input.Failure", inputFailure, nil, "validation"},
		{"poison pill", errors.Join(events.ErrPoisonPill, errors.New("unknown source")), nil, "poison_pill"},
		{"undecodable payload", nil, []byte(`{not json`), "unmarshal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := userRegisteredMsg(t, "e1")
			if tc.raw != nil {
				msg.Value = tc.raw
			}
			reader := &greedyReader{msgs: []kafka.Message{msg}}
			dlq := &fakeWriter{}
			var calls atomic.Int32
			// A long window: a retry would be visible as calls > 1.
			disp := newDisp(reader, dlq, events.WithRetryWindow(5*time.Second))
			events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
				calls.Add(1)
				return tc.err
			})

			_ = runOne(t, disp, reader)

			assert.LessOrEqual(t, calls.Load(), int32(1), "never retried")
			dead := dlq.captured()
			require.Len(t, dead, 1)
			assert.Equal(t, tc.reason, string(findTestHeader(dead[0], "x-dlq-reason")))
		})
	}
}

// A shutdown during the wait between attempts must not lose the message and must
// not wait out the window: the message stays uncommitted for the next consumer.
func TestDispatcher_ShutdownDuringInPlaceRetryLeavesTheMessage(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1"), userRegisteredMsg(t, "e2")}}
	dlq := &fakeWriter{}
	entered := make(chan struct{}, 1)
	disp := newDisp(reader, dlq, events.WithRetryWindow(3*time.Hour), events.WithRetryBackoff(time.Hour, time.Hour))
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		return errors.New("db down")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	<-entered
	cancel()

	err := awaitRun(t, done)
	require.ErrorIs(t, err, events.ErrLeaveUncommitted)
	_, commits := reader.snapshot()
	assert.Empty(t, commits, "nothing committed: the message is redelivered")
	assert.Empty(t, dlq.captured(), "and not dead-lettered: it did not fail for good")
	fetches, _ := reader.snapshot()
	assert.Equal(t, 1, fetches, "the next message is never fetched")
}

// One attempt has a time limit: a handler stuck on a dead dependency fails with
// a typed timeout instead of holding the partition.
func TestDispatcher_MessageTimeoutFailsTheAttemptAndIsRetried(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	dlq := &fakeWriter{}
	var calls atomic.Int32
	disp := newDisp(reader, dlq, events.WithMessageTimeout(20*time.Millisecond), events.WithRetryWindow(5*time.Second))
	events.Handle(disp, func(ctx context.Context, _ *usersv1.UserRegistered) error {
		if calls.Add(1) == 1 {
			<-ctx.Done() // stuck until the limit
			return ctx.Err()
		}
		return nil
	})

	err := runOne(t, disp, reader)

	require.ErrorIs(t, err, context.Canceled)
	assert.EqualValues(t, 2, calls.Load(), "the stuck attempt was cut and retried")
	assert.Empty(t, dlq.captured())
}

// The deprecated retry TIER keeps working, and the new dead-letter-at-once
// rules still apply with it.
func TestDispatcher_LegacyRetryTierStillForwardsRetryableAndNotValidation(t *testing.T) {
	retryable := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e1")}}
	dlq, retry := &fakeWriter{}, &fakeWriter{}
	disp := newDisp(retryable, dlq, events.WithRetry(retry))
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error { return errors.New("transient") })
	_ = runOne(t, disp, retryable)
	assert.Len(t, retry.captured(), 1)
	assert.Empty(t, dlq.captured())

	invalid := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e2")}}
	dlq, retry = &fakeWriter{}, &fakeWriter{}
	disp = newDisp(invalid, dlq, events.WithRetry(retry))
	events.Handle(disp, func(context.Context, *usersv1.UserRegistered) error {
		return commonerrors.New(http.StatusUnprocessableEntity).Reason("X").Message("bad").Build()
	})
	_ = runOne(t, disp, invalid)
	assert.Empty(t, retry.captured(), "a validation failure never goes to the retry tier")
	assert.Len(t, dlq.captured(), 1)
}
