package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/encoding/protojson"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

// --- additional test doubles for the failure-path / commit-gating tests ---

// commitCountingReader serves a fixed message set once and records how many
// times the offset was committed. After the messages are exhausted it blocks on
// ctx so the dispatcher's Run loop parks until the test's timeout fires.
type commitCountingReader struct {
	msgs    []kafka.Message
	i       int
	mu      sync.Mutex
	commits int
}

func (r *commitCountingReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.i >= len(r.msgs) {
		r.mu.Unlock()
		<-ctx.Done()
		r.mu.Lock()
		return kafka.Message{}, ctx.Err()
	}
	m := r.msgs[r.i]
	r.i++
	return m, nil
}

func (r *commitCountingReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits += len(msgs)
	return nil
}

func (r *commitCountingReader) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

// failableWriter is a Writer that captures successful writes and can be told to
// fail every write with a sentinel error.
type failableWriter struct {
	mu       sync.Mutex
	msgs     []kafka.Message
	fail     bool
	attempts int
}

var errWriteBoom = errors.New("kafka write boom")

func (w *failableWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts++
	if w.fail {
		return errWriteBoom
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *failableWriter) captured() []kafka.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]kafka.Message, len(w.msgs))
	copy(out, w.msgs)
	return out
}

func (w *failableWriter) attemptCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.attempts
}

// runUntilSettled runs the dispatcher and waits for the message to be processed
// by polling the reader's commit count and the failure-path write attempts.
// It returns once a deterministic terminal state is reached or the deadline
// passes, so assertions don't race the consumer loop.
func runUntilSettled(t *testing.T, disp *events.Dispatcher, settled func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { _ = disp.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		if settled() {
			cancel()
			<-done
			return
		}
		select {
		case <-deadline:
			cancel()
			<-done
			return
		case <-tick.C:
		}
	}
}

func transientMsg(t *testing.T, retryCount string) kafka.Message {
	t.Helper()
	payload, err := protojson.Marshal(&usersv1.UserRegistered{EventId: "e1", UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		envelope.HeaderEventID:   "e1",
		envelope.HeaderEventType: "events.users.v1.UserRegistered",
	}
	if retryCount != "" {
		headers[envelope.HeaderRetryCount] = retryCount
	}
	return kafka.Message{Headers: kafkaHeaders(headers), Value: payload}
}

// TestDispatcher_failurePath is the C2 regression matrix. It pins the contract
// that a transient handler failure is NEVER silently dropped: it is either
// retried or dead-lettered, and when that forwarding write itself fails the
// source offset is left UNCOMMITTED so Kafka redelivers the message.
func TestDispatcher_failurePath(t *testing.T) {
	tests := []struct {
		name string
		// dispatcher wiring
		withRetry  bool
		retryFails bool
		dlqFails   bool
		retryCount string // x-retry-count header on the inbound message
		// expectations
		wantDLQ            int    // messages landed in DLQ
		wantRetry          int    // messages landed in retry topic
		wantRetryCountHdr  string // x-retry-count on the forwarded retry message ("" = skip)
		wantCommit         int    // 0 => offset left uncommitted (redelivery), 1 => committed
		wantDLQReasonfirst string // x-dlq-reason on the first DLQ message ("" = skip)
	}{
		{
			// C2 core fix: no retry writer + retryCount 0 + transient error
			// => message goes to DLQ instead of being dropped; offset commits.
			name:               "nil retry writer routes transient failure to DLQ and commits",
			withRetry:          false,
			retryCount:         "",
			wantDLQ:            1,
			wantRetry:          0,
			wantCommit:         1,
			wantDLQReasonfirst: "max_retries",
		},
		{
			// DLQ write fails on the nil-retry path => offset NOT committed so
			// the broker redelivers; nothing lands anywhere.
			name:       "nil retry writer + DLQ write fails leaves offset uncommitted",
			withRetry:  false,
			dlqFails:   true,
			retryCount: "",
			wantDLQ:    0,
			wantRetry:  0,
			wantCommit: 0,
		},
		{
			// Retry writer configured, budget remains => forward to retry topic
			// with x-retry-count incremented; offset commits; DLQ untouched.
			name:              "retry writer configured forwards to retry topic with incremented count and commits",
			withRetry:         true,
			retryCount:        "", // RetryCount() == 0
			wantDLQ:           0,
			wantRetry:         1,
			wantRetryCountHdr: "1",
			wantCommit:        1,
		},
		{
			// Retry write fails => offset NOT committed for redelivery; DLQ
			// untouched (we do not double-route).
			name:       "retry write fails leaves offset uncommitted",
			withRetry:  true,
			retryFails: true,
			retryCount: "",
			wantDLQ:    0,
			wantRetry:  0,
			wantCommit: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &commitCountingReader{msgs: []kafka.Message{transientMsg(t, tc.retryCount)}}
			dlq := &failableWriter{fail: tc.dlqFails}
			retry := &failableWriter{fail: tc.retryFails}

			opts := []events.DispatcherOption{events.WithMaxRetries(3)}
			if tc.withRetry {
				opts = append(opts, events.WithRetry(retry))
			}
			disp := events.NewDispatcher(reader, dlq, opts...)
			events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
				return errors.New("transient failure")
			})

			// Settled when the failure path has been taken (a write attempt was
			// made) AND the loop reached its commit decision. For the commit
			// cases that's commitCount==1; for the no-commit cases we wait for
			// the relevant writer to have attempted (and failed) at least once.
			runUntilSettled(t, disp, func() bool {
				if tc.wantCommit == 1 {
					return reader.commitCount() >= 1
				}
				// no-commit cases: the failed write was attempted
				if tc.withRetry {
					return retry.attemptCount() >= 1
				}
				return dlq.attemptCount() >= 1
			})

			if got := len(dlq.captured()); got != tc.wantDLQ {
				t.Errorf("DLQ messages = %d, want %d", got, tc.wantDLQ)
			}
			if got := len(retry.captured()); got != tc.wantRetry {
				t.Errorf("retry messages = %d, want %d", got, tc.wantRetry)
			}
			if got := reader.commitCount(); got != tc.wantCommit {
				t.Errorf("offset commits = %d, want %d", got, tc.wantCommit)
			}
			if tc.wantDLQReasonfirst != "" {
				caps := dlq.captured()
				if len(caps) == 0 {
					t.Fatalf("expected a DLQ message to assert reason, got none")
				}
				if got := string(findTestHeader(caps[0], "x-dlq-reason")); got != tc.wantDLQReasonfirst {
					t.Errorf("x-dlq-reason = %q, want %q", got, tc.wantDLQReasonfirst)
				}
			}
			if tc.wantRetryCountHdr != "" {
				caps := retry.captured()
				if len(caps) == 0 {
					t.Fatalf("expected a retry message to assert x-retry-count, got none")
				}
				if got := string(findTestHeader(caps[0], envelope.HeaderRetryCount)); got != tc.wantRetryCountHdr {
					t.Errorf("x-retry-count = %q, want %q", got, tc.wantRetryCountHdr)
				}
			}
		})
	}
}

// TestDispatcher_poisonPillDLQWriteFails confirms requirement 3: a non-maxRetries
// reason (poison pill) whose DLQ write fails also leaves the offset uncommitted.
func TestDispatcher_poisonPillDLQWriteFails_leavesUncommitted(t *testing.T) {
	reader := &commitCountingReader{msgs: []kafka.Message{transientMsg(t, "")}}
	dlq := &failableWriter{fail: true}

	disp := events.NewDispatcher(reader, dlq)
	events.Handle(disp, func(_ context.Context, _ *usersv1.UserRegistered) error {
		return errors.Join(events.ErrPoisonPill, errors.New("bad data"))
	})

	runUntilSettled(t, disp, func() bool { return dlq.attemptCount() >= 1 })

	if got := len(dlq.captured()); got != 0 {
		t.Errorf("DLQ captured = %d, want 0 (write failed)", got)
	}
	if got := reader.commitCount(); got != 0 {
		t.Errorf("offset commits = %d, want 0 (poison DLQ write failed -> redeliver)", got)
	}
}
