package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- test doubles: a table-like store and a kafka-go-like writer ---

// tableStore behaves like outbox_messages: rows come back oldest first,
// excluded ids are skipped without taking a slot, and marked rows are gone.
type tableStore struct {
	rows     []*Message
	excludes [][]string // the exclude list of every fetch, in order
	marked   []string
}

func (s *tableStore) FetchPendingExcept(_ context.Context, batchSize int, exclude []string) ([]*Message, error) {
	s.excludes = append(s.excludes, slices.Clone(exclude))
	var batch []*Message
	for _, row := range s.rows {
		if slices.Contains(exclude, row.ID) {
			continue
		}
		if len(batch) == batchSize {
			break
		}
		batch = append(batch, row)
	}
	return batch, nil
}

func (s *tableStore) MarkSentBatch(_ context.Context, ids []string) error {
	s.marked = append(s.marked, ids...)
	kept := s.rows[:0]
	for _, row := range s.rows {
		if !slices.Contains(ids, row.ID) {
			kept = append(kept, row)
		}
	}
	s.rows = kept
	return nil
}

func (s *tableStore) add(rows ...*Message) { s.rows = append(s.rows, rows...) }

// poisonWriter fails a WHOLE WriteMessages call with a row's error as soon as
// the call carries a poisoned row — what kafka-go's pre-flight checks (size,
// topic metadata) do. Every other row is accepted. Rows are recognised by
// their payload, which the fixtures set to the row id.
type poisonWriter struct {
	poison  map[string]error
	failAll error // when set, every call fails with it
	calls   [][]string
	onWrite func(ids []string)
}

func (w *poisonWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = string(m.Value)
	}
	w.calls = append(w.calls, ids)
	if w.onWrite != nil {
		w.onWrite(ids)
	}
	if w.failAll != nil {
		return w.failAll
	}
	for _, id := range ids {
		if err, bad := w.poison[id]; bad {
			return err
		}
	}
	return nil
}

func (w *poisonWriter) singleWrites() []string {
	var out []string
	for _, call := range w.calls {
		if len(call) == 1 {
			out = append(out, call[0])
		}
	}
	return out
}

func row(id, key string) *Message {
	return &Message{
		ID:            id,
		AggregateType: "user",
		AggregateID:   "agg-" + id,
		EventType:     "events.users.v1.UserRegistered",
		Topic:         "user.events",
		Key:           key,
		Payload:       []byte(id),
		Status:        StatusPending,
		CreatedAt:     time.Now().UTC(),
	}
}

func parkRelay(store relayStore, writer relayWriter, parkAfter int) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: zap.NewNop(),
		cfg:    RelayConfig{PollInterval: time.Hour, BatchSize: 10, ParkAfterFailures: parkAfter},
	}
}

// --- D19: a batch-level failure no longer wedges the relay ---

// One unpublishable row used to fail the whole batch, and the next poll
// fetched the same rows again: nothing behind it was ever delivered. Now the
// batch is retried row by row, and everything Kafka accepts goes through.
func TestPollAndForward_BatchErrorFallsBackRowByRow(t *testing.T) {
	store := &tableStore{}
	store.add(row("a", ""), row("p", ""), row("c", ""))
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}

	errorsBefore := counterOf(t, "outbox_relay_errors_total")

	processed, err := parkRelay(store, writer, 5).pollAndForward(context.Background())

	require.NoError(t, err, "a partly delivered batch keeps the partial-success return semantics")
	assert.Equal(t, 2, processed)
	assert.Equal(t, []string{"a", "c"}, store.marked, "only the delivered rows may be marked sent")
	assert.Equal(t, [][]string{{"a", "p", "c"}, {"a"}, {"p"}, {"c"}}, writer.calls,
		"one batch call, then every row on its own, in fetch order")
	assert.Equal(t, errorsBefore+1, counterOf(t, "outbox_relay_errors_total"),
		"the undelivered row is an error even though the poll made progress")
}

// The parking itself: a row whose own write fails ParkAfterFailures times in
// a row is no longer fetched, and the relay is back on its batch path.
func TestPollAndForward_ParksPoisonRowAfterNFailures(t *testing.T) {
	store := &tableStore{}
	writer := &poisonWriter{poison: map[string]error{"p": kafka.UnknownTopicOrPartition}}
	relay := parkRelay(store, writer, 3)
	parkedBefore := counterOf(t, "outbox_parked_events_total")

	store.add(row("p", ""), row("a", ""))
	_, err := relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, relay.failures["p"])

	store.add(row("b", ""))
	_, err = relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, relay.failures["p"])
	assert.Equal(t, parkedBefore, counterOf(t, "outbox_parked_events_total"), "not parked before the Nth failure")

	store.add(row("c", ""))
	_, err = relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Contains(t, relay.parked, "p", "the third consecutive failure parks the row")
	assert.NotContains(t, relay.failures, "p")
	assert.Equal(t, parkedBefore+1, counterOf(t, "outbox_parked_events_total"))

	store.add(row("d", ""), row("e", ""))
	writer.calls = nil
	processed, err := relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.Equal(t, []string{"p"}, store.excludes[len(store.excludes)-1], "the fetch must skip the parked row")
	assert.Equal(t, [][]string{{"d", "e"}}, writer.calls, "with the poison parked the batch goes in one call again")

	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, store.marked)
	assert.NotContains(t, store.marked, "p", "a parked row is never marked sent: it stays pending")
	assert.Len(t, store.rows, 1, "the parked row is still in the table")
}

// A batch of ONE row is not written twice: its batch error already is the
// row's own error.
func TestPollAndForward_SingleRowBatchIsNotRewritten(t *testing.T) {
	store := &tableStore{}
	store.add(row("p", ""))
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}
	relay := parkRelay(store, writer, 2)

	_, err := relay.pollAndForward(context.Background())
	require.Error(t, err, "nothing was delivered")
	assert.Len(t, writer.calls, 1)
	assert.Equal(t, 1, relay.failures["p"], "a row-scoped error counts even when the row is alone")

	_, err = relay.pollAndForward(context.Background())
	require.Error(t, err)
	assert.Len(t, writer.calls, 2)
	assert.Contains(t, relay.parked, "p")

	processed, err := relay.pollAndForward(context.Background())
	require.NoError(t, err, "with the row parked the table looks empty: a healthy zero-row poll")
	assert.Zero(t, processed)
	assert.Len(t, writer.calls, 2, "a parked row is never written again")
}

// Nothing delivered → an error, so the poll counts as failed and the
// freshness gauge does not move, exactly as in the kafka.WriteErrors arm.
func TestPollAndForward_FallbackDeliveringNothingReturnsTheBatchError(t *testing.T) {
	store := &tableStore{}
	store.add(row("p1", ""), row("p2", ""))
	batchErr := kafka.MessageTooLargeError{}
	writer := &poisonWriter{poison: map[string]error{"p1": batchErr, "p2": batchErr}}

	processed, err := parkRelay(store, writer, 5).pollAndForward(context.Background())

	require.Error(t, err)
	assert.ErrorAs(t, err, &kafka.MessageTooLargeError{})
	assert.Zero(t, processed)
	assert.Empty(t, store.marked)
}

// A broker that is down is nobody's row: the old behaviour stands — no
// row-by-row pass (which would multiply the timeout by the batch size) and
// nothing counted, however long the outage lasts.
func TestPollAndForward_BrokerWideBatchErrorKeepsTheBatchAndParksNothing(t *testing.T) {
	for name, brokerErr := range map[string]error{
		"connection refused": &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"dropped connection": io.EOF,
		"write timeout":      context.DeadlineExceeded,
		"closed writer":      io.ErrClosedPipe,
	} {
		t.Run(name, func(t *testing.T) {
			store := &tableStore{}
			store.add(row("a", ""), row("b", ""))
			writer := &poisonWriter{failAll: brokerErr}
			relay := parkRelay(store, writer, 2)
			parkedBefore := counterOf(t, "outbox_parked_events_total")

			for range 10 {
				_, err := relay.pollAndForward(context.Background())
				require.ErrorIs(t, err, brokerErr)
			}

			assert.Len(t, writer.calls, 10, "one batch call per poll, never a row-by-row pass")
			assert.Empty(t, relay.parked)
			assert.Empty(t, relay.failures)
			assert.Equal(t, parkedBefore, counterOf(t, "outbox_parked_events_total"))
			assert.Empty(t, store.marked)
		})
	}
}

// A broker-wide error in the MIDDLE of a pass ends it: the remaining rows are
// not attempted (each would wait out the same timeout), and what was
// delivered before is still marked.
func TestPollAndForward_BrokerWideErrorEndsThePass(t *testing.T) {
	store := &tableStore{}
	store.add(row("p", ""), row("a", ""), row("b", ""), row("c", ""))
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}
	writer.onWrite = func(ids []string) {
		if slices.Equal(ids, []string{"b"}) {
			writer.failAll = io.EOF // the broker goes away while the pass runs
		}
	}

	processed, err := parkRelay(store, writer, 5).pollAndForward(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, []string{"a"}, store.marked)
	assert.Equal(t, []string{"p", "a", "b"}, writer.singleWrites(), "c is never attempted after the broker-wide error")
}

// An unclassified error proves nothing about the row by itself — a SASL
// failure, say, hits every row. It counts only when another row of the same
// pass was delivered, which proves the broker is fine.
func TestPollAndForward_UnclassifiedErrorCountsOnlyWhenSomethingWasDelivered(t *testing.T) {
	t.Run("nothing delivered: never parks", func(t *testing.T) {
		store := &tableStore{}
		store.add(row("a", ""), row("b", ""))
		writer := &poisonWriter{failAll: errors.New("SASL authentication failed")}
		relay := parkRelay(store, writer, 2)

		for range 10 {
			_, err := relay.pollAndForward(context.Background())
			require.Error(t, err)
		}

		assert.Empty(t, relay.parked, "an error every row shares must never park healthy rows")
		assert.Empty(t, relay.failures)
	})

	t.Run("another row delivered: parks", func(t *testing.T) {
		store := &tableStore{}
		writer := &poisonWriter{poison: map[string]error{"p": errors.New("something only this row hits")}}
		relay := parkRelay(store, writer, 2)

		store.add(row("p", ""), row("a", ""))
		_, err := relay.pollAndForward(context.Background())
		require.NoError(t, err)
		store.add(row("b", ""))
		_, err = relay.pollAndForward(context.Background())
		require.NoError(t, err)

		assert.Contains(t, relay.parked, "p")
	})
}

// Kafka's temporary codes (a leader election, a request timeout) heal by
// themselves; they never move a row toward parking, even next to deliveries.
func TestPollAndForward_TransientKafkaErrorNeverParks(t *testing.T) {
	store := &tableStore{}
	writer := &poisonWriter{poison: map[string]error{"p": kafka.LeaderNotAvailable}}
	relay := parkRelay(store, writer, 2)

	store.add(row("p", ""))
	for i := range 10 {
		store.add(row(string(rune('a'+i)), ""))
		_, err := relay.pollAndForward(context.Background())
		require.NoError(t, err)
	}

	assert.Empty(t, relay.parked)
	assert.Empty(t, relay.failures)
}

// Per-key order is kept: once a row fails, the later rows with the same topic
// and key wait — delivering them first would hand a consumer that aggregate's
// events out of order. Rows with other keys (or no key) still go.
func TestPollAndForward_HoldsBackSameKeyRowsBehindAFailedRow(t *testing.T) {
	store := &tableStore{}
	store.add(row("a1", "K"), row("a2", "K"), row("b1", "L"), row("n1", ""))
	writer := &poisonWriter{poison: map[string]error{"a1": kafka.UnknownTopicOrPartition}}
	relay := parkRelay(store, writer, 1)

	processed, err := relay.pollAndForward(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.Equal(t, []string{"a1", "b1", "n1"}, writer.singleWrites(), "a2 is never attempted while a1 is undelivered")
	assert.Equal(t, []string{"b1", "n1"}, store.marked)
	assert.Contains(t, relay.parked, "a1", "ParkAfterFailures=1 parks on the first failure")

	// With a1 parked, its key's next row flows.
	processed, err = relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, []string{"b1", "n1", "a2"}, store.marked)
}

// A shutdown during the row-by-row pass stops it between rows: no new row is
// written after the cancel, and the rows already delivered are marked sent.
func TestPollAndForward_ShutdownMidFallbackMarksDeliveredRows(t *testing.T) {
	store := &tableStore{}
	store.add(row("a", ""), row("b", ""), row("p", ""))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}
	writer.onWrite = func(ids []string) {
		if slices.Equal(ids, []string{"a"}) {
			cancel()
		}
	}

	processed, err := parkRelay(store, writer, 5).pollAndForward(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, []string{"a"}, store.marked, "the delivered row is marked despite the cancel")
	assert.Equal(t, []string{"a"}, writer.singleWrites(), "nothing is written after the cancel")
}

// Counts are CONSECUTIVE failures of rows still in the batch: a row the next
// fetch does not return (delivered by another replica, deleted) loses its
// count, so the map stays bounded by the batch size.
func TestPollAndForward_FailureCountsOnlyKeepRowsOfTheLatestBatch(t *testing.T) {
	store := &tableStore{}
	store.add(row("p", ""), row("a", ""))
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}
	relay := parkRelay(store, writer, 5)

	_, err := relay.pollAndForward(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, relay.failures["p"])

	store.rows = nil // another replica delivered p
	_, err = relay.pollAndForward(context.Background())
	require.NoError(t, err)
	assert.Empty(t, relay.failures)
}

// The kafka.WriteErrors arm is unchanged: per-entry failures are retried on
// the next poll as before and never parked — in that arm the rest of the
// batch is delivered, so a poison row there wedges nothing.
func TestPollAndForward_WriteErrorsArmIsUnchanged(t *testing.T) {
	store := &tableStore{}
	store.add(row("a", ""), row("p", ""))
	writer := &scriptedWriter{err: kafka.WriteErrors{nil, kafka.MessageSizeTooLarge}}
	relay := parkRelay(store, writer, 1)

	processed, err := relay.pollAndForward(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 1, writer.calls, "no row-by-row pass after kafka.WriteErrors")
	assert.Equal(t, []string{"a"}, store.marked)
	assert.Empty(t, relay.parked)
}

// The parked set is bounded: every parked id rides on every fetch, so a cause
// that poisons every row must not grow it forever. At the cap the row is
// retried each poll instead — the behaviour from before parking existed.
func TestPollAndForward_ParkedSetIsCapped(t *testing.T) {
	store := &tableStore{}
	store.add(row("p", ""), row("a", ""))
	writer := &poisonWriter{poison: map[string]error{"p": kafka.MessageTooLargeError{}}}
	relay := parkRelay(store, writer, 1)
	relay.parked = make(map[string]struct{}, MaxParkedRows)
	for i := range MaxParkedRows {
		relay.parked[fmt.Sprintf("old-%d", i)] = struct{}{}
	}
	parkedBefore := counterOf(t, "outbox_parked_events_total")

	_, err := relay.pollAndForward(context.Background())

	require.NoError(t, err)
	assert.Len(t, relay.parked, MaxParkedRows, "the cap holds")
	assert.NotContains(t, relay.parked, "p")
	assert.Equal(t, 1, relay.failures["p"], "the row keeps its count and is retried next poll")
	assert.Equal(t, parkedBefore, counterOf(t, "outbox_parked_events_total"))
	assert.Equal(t, []string{"a"}, store.marked, "the healthy row is still delivered")
}

func TestClassifyRowFailure(t *testing.T) {
	cases := map[string]struct {
		err  error
		want rowFailureClass
	}{
		"client-side size check":      {kafka.MessageTooLargeError{}, failureRowScoped},
		"unknown topic":               {kafka.UnknownTopicOrPartition, failureRowScoped},
		"invalid topic":               {kafka.InvalidTopic, failureRowScoped},
		"topic not authorised":        {kafka.TopicAuthorizationFailed, failureRowScoped},
		"broker size limit":           {kafka.MessageSizeTooLarge, failureRowScoped},
		"record list too large":       {kafka.RecordListTooLarge, failureRowScoped},
		"invalid record":              {kafka.InvalidRecord, failureRowScoped},
		"leader election":             {kafka.LeaderNotAvailable, failureTransient},
		"request timed out":           {kafka.RequestTimedOut, failureTransient},
		"sasl (non-temporary code)":   {kafka.SASLAuthenticationFailed, failureUnclassified},
		"plain error":                 {errors.New("boom"), failureUnclassified},
		"cancelled":                   {context.Canceled, failureBrokerWide},
		"deadline":                    {context.DeadlineExceeded, failureBrokerWide},
		"closed writer":               {io.ErrClosedPipe, failureBrokerWide},
		"eof":                         {io.EOF, failureBrokerWide},
		"unexpected eof":              {io.ErrUnexpectedEOF, failureBrokerWide},
		"network":                     {&net.OpError{Op: "dial", Err: errors.New("refused")}, failureBrokerWide},
		"wrapped unknown topic":       {errors.Join(errors.New("ctx"), kafka.UnknownTopicOrPartition), failureRowScoped},
		"single-entry WriteErrors in": {singleWriteError(kafka.WriteErrors{kafka.InvalidTopic}), failureRowScoped},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyRowFailure(tc.err))
		})
	}
}

func TestRelayConfig_ParkAfterFailuresDefault(t *testing.T) {
	assert.Equal(t, DefaultParkAfterFailures, parkRelay(nil, nil, 0).parkAfterFailures(),
		"a hand-built zero RelayConfig must fall back to the default, never switch parking off")
	assert.Equal(t, DefaultParkAfterFailures, parkRelay(nil, nil, -1).parkAfterFailures())
	assert.Equal(t, 7, parkRelay(nil, nil, 7).parkAfterFailures())
	assert.Equal(t, 5, DefaultConfig().Relay.ParkAfterFailures)
}

func TestDefaultConfig_ParkAfterFailuresFromEnv(t *testing.T) {
	t.Setenv("OUTBOX_PARK_AFTER_FAILURES", "9")
	assert.Equal(t, 9, DefaultConfig().Relay.ParkAfterFailures)
}
