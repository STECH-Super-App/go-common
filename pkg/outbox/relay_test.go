package outbox

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// fakeRelayStore is an in-memory relayStore that records every call so tests
// can assert the exact claim / mark-sent / release sequence without Postgres.
type fakeRelayStore struct {
	claimResult []*Message
	claimErr    error

	markSentCalls [][]string
	markSentErr   error

	releaseCalls [][]string
	releaseErr   error
}

func (s *fakeRelayStore) ClaimPending(_ context.Context, _ int) ([]*Message, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.claimResult, nil
}

func (s *fakeRelayStore) MarkSentBatch(_ context.Context, ids []string) error {
	s.markSentCalls = append(s.markSentCalls, ids)
	return s.markSentErr
}

func (s *fakeRelayStore) ReleaseBatch(_ context.Context, ids []string) error {
	s.releaseCalls = append(s.releaseCalls, ids)
	return s.releaseErr
}

// fakeWriter is an in-memory messageWriter returning a scripted error.
type fakeWriter struct {
	err   error
	calls [][]kafka.Message
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.calls = append(w.calls, msgs)
	return w.err
}

func testMessages(ids ...string) []*Message {
	out := make([]*Message, len(ids))
	for i, id := range ids {
		out[i] = &Message{ID: id, Topic: "test.events", Payload: []byte(`{}`)}
	}
	return out
}

func newTestRelay(store *fakeRelayStore, writer *fakeWriter) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: zap.NewNop(),
		cfg:    RelayConfig{BatchSize: 10},
	}
}

func assertIDCalls(t *testing.T, name string, got [][]string, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s calls = %v, want %v", name, got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s call %d = %v, want %v", name, i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("%s call %d = %v, want %v", name, i, got[i], want[i])
			}
		}
	}
}

func TestRelay_pollAndForward_success(t *testing.T) {
	store := &fakeRelayStore{claimResult: testMessages("id-1", "id-2")}
	writer := &fakeWriter{}
	r := newTestRelay(store, writer)

	n, err := r.pollAndForward(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if n != 2 {
		t.Errorf("processed = %d, want 2", n)
	}
	assertIDCalls(t, "MarkSentBatch", store.markSentCalls, [][]string{{"id-1", "id-2"}})
	assertIDCalls(t, "ReleaseBatch", store.releaseCalls, nil)
	if len(writer.calls) != 1 || len(writer.calls[0]) != 2 {
		t.Errorf("writer calls = %v, want one call with 2 messages", writer.calls)
	}
}

func TestRelay_pollAndForward_emptyClaimWritesNothing(t *testing.T) {
	store := &fakeRelayStore{}
	writer := &fakeWriter{}
	r := newTestRelay(store, writer)

	n, err := r.pollAndForward(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", n, err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("writer must not be called for an empty claim")
	}
}

func TestRelay_pollAndForward_claimErrorPropagates(t *testing.T) {
	sentinel := errors.New("claim failed")
	store := &fakeRelayStore{claimErr: sentinel}
	r := newTestRelay(store, &fakeWriter{})

	_, err := r.pollAndForward(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(%v)", err, sentinel)
	}
}

// TestRelay_pollAndForward_wholeBatchFailureReleasesClaim is the core
// claim-lifecycle regression: a whole-batch Kafka failure (broker down) must
// return every claimed row to 'pending' immediately — not leave it stuck in
// 'processing' until the reaper's ClaimTimeout.
func TestRelay_pollAndForward_wholeBatchFailureReleasesClaim(t *testing.T) {
	sentinel := errors.New("broker down")
	store := &fakeRelayStore{claimResult: testMessages("id-1", "id-2")}
	writer := &fakeWriter{err: sentinel}
	r := newTestRelay(store, writer)

	n, err := r.pollAndForward(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(%v)", err, sentinel)
	}
	if n != 0 {
		t.Errorf("processed = %d, want 0", n)
	}
	assertIDCalls(t, "ReleaseBatch", store.releaseCalls, [][]string{{"id-1", "id-2"}})
	assertIDCalls(t, "MarkSentBatch", store.markSentCalls, nil)
}

func TestRelay_pollAndForward_partialFailureMarksAndReleases(t *testing.T) {
	store := &fakeRelayStore{claimResult: testMessages("id-1", "id-2", "id-3")}
	// kafka.WriteErrors carries one entry per message: id-2 failed.
	writer := &fakeWriter{err: kafka.WriteErrors{nil, errors.New("partition offline"), nil}}
	r := newTestRelay(store, writer)

	n, err := r.pollAndForward(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil (partial failure retries next poll)", err)
	}
	if n != 2 {
		t.Errorf("processed = %d, want 2", n)
	}
	assertIDCalls(t, "MarkSentBatch", store.markSentCalls, [][]string{{"id-1", "id-3"}})
	assertIDCalls(t, "ReleaseBatch", store.releaseCalls, [][]string{{"id-2"}})
}

func TestRelay_pollAndForward_allEntriesFailedReleasesAll(t *testing.T) {
	store := &fakeRelayStore{claimResult: testMessages("id-1", "id-2")}
	writer := &fakeWriter{err: kafka.WriteErrors{errors.New("e1"), errors.New("e2")}}
	r := newTestRelay(store, writer)

	n, err := r.pollAndForward(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", n, err)
	}
	assertIDCalls(t, "ReleaseBatch", store.releaseCalls, [][]string{{"id-1", "id-2"}})
	assertIDCalls(t, "MarkSentBatch", store.markSentCalls, nil)
}

// TestRelay_pollAndForward_markSentFailureReleases: rows already published to
// Kafka whose mark-sent UPDATE failed must be released for an immediate
// at-least-once retry instead of sitting in 'processing' until ClaimTimeout.
func TestRelay_pollAndForward_markSentFailureReleases(t *testing.T) {
	sentinel := errors.New("db gone")
	store := &fakeRelayStore{
		claimResult: testMessages("id-1"),
		markSentErr: sentinel,
	}
	r := newTestRelay(store, &fakeWriter{})

	_, err := r.pollAndForward(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(%v)", err, sentinel)
	}
	assertIDCalls(t, "ReleaseBatch", store.releaseCalls, [][]string{{"id-1"}})
}

// TestRelay_pollAndForward_releaseFailureDoesNotMaskWriteError: when both the
// Kafka write and the release fail, the write error is what propagates; the
// release failure is only logged (the reaper backstop recovers the rows).
func TestRelay_pollAndForward_releaseFailureDoesNotMaskWriteError(t *testing.T) {
	writeErr := errors.New("broker down")
	store := &fakeRelayStore{
		claimResult: testMessages("id-1"),
		releaseErr:  errors.New("db also down"),
	}
	writer := &fakeWriter{err: writeErr}
	r := newTestRelay(store, writer)

	_, err := r.pollAndForward(context.Background())
	if !errors.Is(err, writeErr) {
		t.Fatalf("err = %v, want errors.Is(%v)", err, writeErr)
	}
}
