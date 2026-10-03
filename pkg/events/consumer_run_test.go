package events_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/events"
)

// scriptedFetchReader answers FetchMessage from a script; past the end of the
// script it repeats the last step. Every call is timestamped.
type scriptedFetchReader struct {
	mu     sync.Mutex
	script []fetchStep
	calls  []time.Time
}

type fetchStep struct {
	msg kafka.Message
	err error
}

func (r *scriptedFetchReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if err := ctx.Err(); err != nil {
		return kafka.Message{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := len(r.calls)
	r.calls = append(r.calls, time.Now())
	if idx >= len(r.script) {
		idx = len(r.script) - 1
	}
	step := r.script[idx]
	return step.msg, step.err
}

func (r *scriptedFetchReader) CommitMessages(context.Context, ...kafka.Message) error { return nil }

func (r *scriptedFetchReader) callTimes() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.calls...)
}

var errBrokerDown = errors.New("dial tcp: connection refused")

// A closed kafka-go reader answers every FetchMessage with io.EOF. Run must
// treat that as a shutdown and return nil — before this it `continue`d and
// spun a CPU core for as long as the process lived.
func TestDispatcherRun_ClosedReaderReturnsNil(t *testing.T) {
	reader := &scriptedFetchReader{script: []fetchStep{{err: io.EOF}}}
	disp := events.NewDispatcher(reader, &fakeWriter{})

	done := make(chan error, 1)
	go func() { done <- disp.Run(context.Background()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on a closed reader")
	}
	assert.Len(t, reader.callTimes(), 1, "a closed reader must not be polled again")
}

// A wrapped io.EOF is still a closed reader.
func TestDispatcherRun_WrappedEOFReturnsNil(t *testing.T) {
	reader := &scriptedFetchReader{script: []fetchStep{{err: errors.Join(errors.New("fetch"), io.EOF)}}}
	err := events.NewDispatcher(reader, &fakeWriter{}).Run(context.Background())
	require.NoError(t, err)
}

// Any other fetch error is retried with a doubling backoff starting at
// 100 ms: in ~650 ms that is calls at ≈0, 100, 300 ms (the next is due at
// 700) — never the thousands a hot loop makes.
func TestDispatcherRun_FetchErrorsBackOff(t *testing.T) {
	reader := &scriptedFetchReader{script: []fetchStep{{err: errBrokerDown}}}
	disp := events.NewDispatcher(reader, &fakeWriter{})

	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	err := disp.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	calls := reader.callTimes()
	assert.GreaterOrEqual(t, len(calls), 3, "the dispatcher must keep retrying")
	assert.LessOrEqual(t, len(calls), 4, "fetch errors must back off, not hot-loop")
	if len(calls) >= 3 {
		assert.GreaterOrEqual(t, calls[1].Sub(calls[0]), 90*time.Millisecond)
		assert.GreaterOrEqual(t, calls[2].Sub(calls[1]), 190*time.Millisecond, "the backoff must double")
	}
}

// A successful fetch resets the backoff: after three failures (waits 100,
// 200, 400 ms) a message arrives, and the next failure waits 100 ms again —
// not the 800 ms the un-reset sequence would have reached.
func TestDispatcherRun_SuccessResetsBackoff(t *testing.T) {
	reader := &scriptedFetchReader{script: []fetchStep{
		{err: errBrokerDown},
		{err: errBrokerDown},
		{err: errBrokerDown},
		{msg: kafka.Message{Topic: "user.events"}}, // no handler: skipped and committed
		{err: errBrokerDown},
		{err: errBrokerDown},
	}}
	disp := events.NewDispatcher(reader, &fakeWriter{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	require.Eventually(t, func() bool { return len(reader.callTimes()) >= 6 },
		3*time.Second, 10*time.Millisecond)
	cancel()
	<-done

	calls := reader.callTimes()
	// calls[3] is the message, calls[4] the failure right after it (no wait
	// after a success), calls[5] the retry after that failure's backoff.
	gap := calls[5].Sub(calls[4])
	assert.GreaterOrEqual(t, gap, 90*time.Millisecond)
	assert.Less(t, gap, 400*time.Millisecond, "a successful fetch must reset the backoff to 100 ms")
}

// Cancellation during a backoff wait ends Run at once with ctx.Err().
func TestDispatcherRun_CancelDuringBackoffReturnsPromptly(t *testing.T) {
	reader := &scriptedFetchReader{script: []fetchStep{
		{err: errBrokerDown}, {err: errBrokerDown}, {err: errBrokerDown},
		{err: errBrokerDown}, {err: errBrokerDown}, {err: errBrokerDown},
	}}
	disp := events.NewDispatcher(reader, &fakeWriter{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	// After 5 failures the wait is 1.6 s; cancel inside it.
	require.Eventually(t, func() bool { return len(reader.callTimes()) >= 5 },
		3*time.Second, 5*time.Millisecond)
	cancelled := time.Now()
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(cancelled), 200*time.Millisecond)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel during backoff")
	}
}
