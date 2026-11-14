package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- test doubles for the relay seam ---

// fakeRelayStore is an in-memory relayStore. Like Postgres it refuses work on a
// done context, claims a topic's rows oldest first, and removes from pending
// only what the SendFunc acknowledged.
type fakeRelayStore struct {
	mu        sync.Mutex
	pending   []*Message
	listErr   error
	claimErr  error
	markedIDs []string
	onList    func(call int) // called by PendingTopics, with the 1-based call number
	onClaim   func()         // called after a topic's rows are claimed, before they are sent
	listCalls int
	lastLimit int
	forwarded []string // topics ForwardTopic was called for, in order
}

func (s *fakeRelayStore) PendingTopics(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.listCalls++
	call, hook, listErr := s.listCalls, s.onList, s.listErr
	var topics []string
	seen := map[string]bool{}
	for _, m := range s.pending {
		if !seen[m.Topic] {
			seen[m.Topic] = true
			topics = append(topics, m.Topic)
		}
	}
	s.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	if listErr != nil {
		return nil, listErr
	}
	return topics, nil
}

func (s *fakeRelayStore) ForwardTopic(ctx context.Context, topic string, limit int, send SendFunc) (ForwardResult, error) {
	if err := ctx.Err(); err != nil {
		return ForwardResult{}, err
	}
	s.mu.Lock()
	s.forwarded = append(s.forwarded, topic)
	s.lastLimit = limit
	claimErr, hook := s.claimErr, s.onClaim
	var claimed []*Message
	for _, m := range s.pending {
		if m.Topic == topic && len(claimed) < limit {
			claimed = append(claimed, m)
		}
	}
	s.mu.Unlock()
	if claimErr != nil {
		return ForwardResult{}, claimErr
	}
	if len(claimed) == 0 {
		return ForwardResult{}, nil
	}
	if hook != nil {
		hook()
	}
	delivered, sendErr := send(ctx, claimed)
	res := ForwardResult{Claimed: len(claimed)}
	if len(delivered) > 0 {
		if err := ctx.Err(); err != nil {
			return res, err // Postgres cannot mark on a done context
		}
		s.mu.Lock()
		gone := map[string]bool{}
		for _, id := range delivered {
			gone[id] = true
		}
		kept := s.pending[:0:0]
		for _, m := range s.pending {
			if !gone[m.ID] {
				kept = append(kept, m)
			}
		}
		s.pending = kept
		s.markedIDs = append(s.markedIDs, delivered...)
		s.mu.Unlock()
		res.Sent = len(delivered)
	}
	return res, sendErr
}

func (s *fakeRelayStore) snapshot() (lists int, marked []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls, append([]string(nil), s.markedIDs...)
}

// scriptedWriter returns a fixed error for every WriteMessages call.
type scriptedWriter struct {
	err   error
	calls int
}

func (w *scriptedWriter) WriteMessages(_ context.Context, _ ...kafka.Message) error {
	w.calls++
	return w.err
}

func testRelay(store relayStore, writer relayWriter) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: zap.NewNop(),
		cfg:    RelayConfig{PollInterval: time.Hour, BatchSize: 10},
	}
}

func pendingMessagesFixture(n int) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = &Message{
			ID:        string(rune('a'+i)) + "-id",
			Key:       string(rune('a'+i)) + "-id", // one key per row: independent aggregates
			EventType: "events.users.v1.UserRegistered",
			Topic:     "user.events",
			Status:    StatusPending,
			CreatedAt: time.Now().UTC(),
		}
	}
	return msgs
}

// A topic in which EVERY message failed must report an error, not a
// zero-message success. Reporting success here refreshes the freshness gauge
// and leaves the error counter flat while nothing was delivered — a relay
// wedged by a missing Kafka topic would look perfectly healthy on every panel
// (Critical Rule 13).
func TestPoll_AllFailedBatchReportsFailure(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(2)}
	writer := &scriptedWriter{err: kafka.WriteErrors{
		errors.New("unknown topic or partition"),
		errors.New("unknown topic or partition"),
	}}

	res := testRelay(store, writer).poll(context.Background())

	assert.True(t, res.failed, "an all-failed batch must not report an error-free poll")
	assert.Zero(t, res.sent)
	assert.False(t, res.more, "a failing topic must not make the relay poll again at once")
	_, marked := store.snapshot()
	assert.Empty(t, marked, "no row may be marked sent when nothing was delivered")
}

// End-to-end through Run: the counters must tell the truth after a poll in
// which every message failed.
func TestRun_AllFailedBatch_IncrementsErrorsAndDoesNotRefreshFreshness(t *testing.T) {
	var freshnessAfterSeed float64

	store := &fakeRelayStore{pending: pendingMessagesFixture(2)}
	writer := &scriptedWriter{err: kafka.WriteErrors{
		errors.New("unknown topic or partition"),
		errors.New("unknown topic or partition"),
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Sample the gauge at the moment of the first poll — i.e. AFTER Run's
	// start-up seed, so the assertion isolates the poll's own effect — then
	// cancel so exactly one poll cycle executes.
	store.onList = func(call int) {
		if call == 1 {
			freshnessAfterSeed = gaugeValue(t, "outbox_last_success_timestamp_seconds")
		}
	}
	store.onClaim = cancel // the shutdown arrives with the batch claimed: it is still finished

	errorsBefore := counterOf(t, "outbox_relay_errors_total")
	relayedBefore := counterOf(t, "outbox_relayed_events_total")

	_ = testRelay(store, writer).Run(ctx)

	lists, _ := store.snapshot()
	require.Equal(t, 1, lists, "the test must observe exactly one poll cycle")
	assert.Equal(t, errorsBefore+1, counterOf(t, "outbox_relay_errors_total"),
		"a batch that delivered nothing must move the error counter")
	assert.Equal(t, relayedBefore, counterOf(t, "outbox_relayed_events_total"),
		"nothing was delivered, so nothing may be counted as relayed")
	assert.Equal(t, freshnessAfterSeed, gaugeValue(t, "outbox_last_success_timestamp_seconds"),
		"a failed poll must NOT refresh outbox_last_success_timestamp_seconds")
}

// Partial delivery is progress AND an error: the delivered half moves the
// relayed counters, the failed half moves the error counters. A relay losing a
// steady fraction of every batch must not report as clean.
func TestPoll_PartialFailureCountsBothOutcomes(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(3)}
	writer := &scriptedWriter{err: kafka.WriteErrors{
		nil,
		errors.New("message too large"),
		nil,
	}}

	topicErrBefore := topicCounterOf(t, "outbox_topic_relay_errors_total", "user.events")
	topicSentBefore := topicCounterOf(t, "outbox_topic_relayed_events_total", "user.events")

	res := testRelay(store, writer).poll(context.Background())

	assert.True(t, res.failed, "the failed half is an error")
	assert.Equal(t, 2, res.sent, "rows 0 and 2 are delivered; row 1 failed and stays pending")
	_, marked := store.snapshot()
	assert.Len(t, marked, res.sent, "only the delivered rows may be marked sent")
	assert.Equal(t, topicErrBefore+1, topicCounterOf(t, "outbox_topic_relay_errors_total", "user.events"))
	assert.Equal(t, topicSentBefore+float64(res.sent), topicCounterOf(t, "outbox_topic_relayed_events_total", "user.events"))
}

// A full success still records freshness and the relayed count through Run.
func TestRun_SuccessfulBatchRefreshesFreshnessAndCountsRelayed(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(2)}
	writer := &scriptedWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onClaim = cancel

	errorsBefore := counterOf(t, "outbox_relay_errors_total")
	relayedBefore := counterOf(t, "outbox_relayed_events_total")

	_ = testRelay(store, writer).Run(ctx)

	assert.Equal(t, relayedBefore+2, counterOf(t, "outbox_relayed_events_total"))
	assert.Equal(t, errorsBefore, counterOf(t, "outbox_relay_errors_total"))
	assert.InDelta(t, float64(time.Now().Unix()),
		gaugeValue(t, "outbox_last_success_timestamp_seconds"), 5)
}

// A topic-list failure is an error poll: error counter up, freshness untouched.
func TestRun_ListErrorIncrementsErrorsAndDoesNotRefreshFreshness(t *testing.T) {
	var freshnessAfterSeed float64

	store := &fakeRelayStore{listErr: errors.New("connection refused")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onList = func(call int) {
		if call == 1 {
			freshnessAfterSeed = gaugeValue(t, "outbox_last_success_timestamp_seconds")
			time.AfterFunc(50*time.Millisecond, cancel) // PollInterval is an hour: one cycle only
		}
	}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")

	_ = testRelay(store, &scriptedWriter{}).Run(ctx)

	assert.Equal(t, errorsBefore+1, counterOf(t, "outbox_relay_errors_total"))
	assert.Equal(t, freshnessAfterSeed, gaugeValue(t, "outbox_last_success_timestamp_seconds"))
}

// An empty table is a healthy poll: freshness refreshes, nothing else moves.
// Without this the "relay stalled" alert would fire on every idle service.
func TestRun_ZeroRowPollRefreshesFreshness(t *testing.T) {
	store := &fakeRelayStore{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onList = func(call int) {
		if call == 1 {
			cancel()
		}
	}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")
	relayedBefore := counterOf(t, "outbox_relayed_events_total")

	_ = testRelay(store, &scriptedWriter{}).Run(ctx)

	assert.Equal(t, errorsBefore, counterOf(t, "outbox_relay_errors_total"))
	assert.Equal(t, relayedBefore, counterOf(t, "outbox_relayed_events_total"))
	assert.InDelta(t, float64(time.Now().Unix()),
		gaugeValue(t, "outbox_last_success_timestamp_seconds"), 5)
}
