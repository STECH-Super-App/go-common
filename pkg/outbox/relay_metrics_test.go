package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- test doubles for the relay seam ---

type fakeRelayStore struct {
	pending    []*Message
	fetchErr   error
	markedIDs  []string
	markErr    error
	onFetch    func(call int)
	fetchCalls int
}

func (s *fakeRelayStore) FetchPending(_ context.Context, _ int) ([]*Message, error) {
	s.fetchCalls++
	if s.onFetch != nil {
		s.onFetch(s.fetchCalls)
	}
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	return s.pending, nil
}

func (s *fakeRelayStore) MarkSentBatch(_ context.Context, ids []string) error {
	if s.markErr != nil {
		return s.markErr
	}
	s.markedIDs = append(s.markedIDs, ids...)
	return nil
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
			EventType: "events.users.v1.UserRegistered",
			Topic:     "user.events",
			Status:    StatusPending,
			CreatedAt: time.Now().UTC(),
		}
	}
	return msgs
}

// A batch in which EVERY message failed must report an error, not a
// zero-message success. Reporting success here refreshes the freshness gauge
// and leaves the error counter flat while nothing was delivered — a relay
// wedged by a missing Kafka topic would look perfectly healthy on every panel
// (Critical Rule 13).
func TestPollAndForward_AllFailedBatchReturnsError(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(2)}
	writer := &scriptedWriter{err: kafka.WriteErrors{
		errors.New("unknown topic or partition"),
		errors.New("unknown topic or partition"),
	}}

	processed, err := testRelay(store, writer).pollAndForward(context.Background())

	require.Error(t, err, "an all-failed batch must not report an error-free poll")
	assert.Zero(t, processed)
	assert.Empty(t, store.markedIDs, "no row may be marked sent when nothing was delivered")
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

	// Sample the gauge at the moment of the first fetch — i.e. AFTER Run's
	// start-up seed, so the assertion isolates the poll's own effect — then
	// cancel so exactly one poll cycle executes.
	store.onFetch = func(call int) {
		if call == 1 {
			freshnessAfterSeed = gaugeValue(t, "outbox_last_success_timestamp_seconds")
			cancel()
		}
	}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")
	relayedBefore := counterOf(t, "outbox_relayed_events_total")

	_ = testRelay(store, writer).Run(ctx)

	require.Equal(t, 1, store.fetchCalls, "the test must observe exactly one poll cycle")
	assert.Equal(t, errorsBefore+1, counterOf(t, "outbox_relay_errors_total"),
		"a batch that delivered nothing must move the error counter")
	assert.Equal(t, relayedBefore, counterOf(t, "outbox_relayed_events_total"),
		"nothing was delivered, so nothing may be counted as relayed")
	assert.Equal(t, freshnessAfterSeed, gaugeValue(t, "outbox_last_success_timestamp_seconds"),
		"a failed poll must NOT refresh outbox_last_success_timestamp_seconds")
}

// Partial delivery is progress AND an error: the succeeded half moves the
// relayed counter, the failed half moves the error counter. A relay losing a
// steady fraction of every batch must not report as clean.
func TestPollAndForward_PartialFailureCountsBothOutcomes(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(3)}
	writer := &scriptedWriter{err: kafka.WriteErrors{
		nil,
		errors.New("message too large"),
		nil,
	}}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")

	processed, err := testRelay(store, writer).pollAndForward(context.Background())

	require.NoError(t, err, "partial success keeps the existing return semantics")
	assert.Equal(t, 2, processed)
	assert.Len(t, store.markedIDs, 2, "only the delivered rows may be marked sent")
	assert.Equal(t, errorsBefore+1, counterOf(t, "outbox_relay_errors_total"),
		"the failed half of a partial batch must move the error counter")
}

// A full success still records freshness and the relayed count through Run.
func TestRun_SuccessfulBatchRefreshesFreshnessAndCountsRelayed(t *testing.T) {
	store := &fakeRelayStore{pending: pendingMessagesFixture(2)}
	writer := &scriptedWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onFetch = func(call int) {
		if call == 1 {
			cancel()
		}
	}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")
	relayedBefore := counterOf(t, "outbox_relayed_events_total")

	_ = testRelay(store, writer).Run(ctx)

	assert.Equal(t, relayedBefore+2, counterOf(t, "outbox_relayed_events_total"))
	assert.Equal(t, errorsBefore, counterOf(t, "outbox_relay_errors_total"))
	assert.InDelta(t, float64(time.Now().Unix()),
		gaugeValue(t, "outbox_last_success_timestamp_seconds"), 5)
}

// A fetch failure is an error poll: error counter up, freshness untouched.
func TestRun_FetchErrorIncrementsErrorsAndDoesNotRefreshFreshness(t *testing.T) {
	var freshnessAfterSeed float64

	store := &fakeRelayStore{fetchErr: errors.New("connection refused")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onFetch = func(call int) {
		if call == 1 {
			freshnessAfterSeed = gaugeValue(t, "outbox_last_success_timestamp_seconds")
			cancel()
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
	store.onFetch = func(call int) {
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
