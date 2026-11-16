package outbox

// Relay behaviour against a REAL Postgres: the claim, the per-topic send and the
// single transaction that marks (release 2: SL-02, SL-03). Kafka is a fake
// writer: the properties under test are the database's.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/internal/testpg"
)

func insertTopicMessage(t *testing.T, pool *pgxpool.Pool, store *Store, id, topic, key string, createdAt time.Time) {
	t.Helper()
	msg := &Message{
		ID: id, AggregateType: "a", AggregateID: key, EventType: "events.x.v1.X",
		Topic: topic, Key: key, Payload: []byte(`{}`),
		Headers: map[string]string{"event_id": id}, Status: StatusPending, CreatedAt: createdAt,
	}
	require.NoError(t, RunInTx(context.Background(), pool, func(tx Tx) error {
		return store.InsertTx(context.Background(), tx, msg)
	}))
}

func statusOf(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM outbox_messages WHERE id = $1`, id).Scan(&s))
	return s
}

// topicWriter is a Kafka fake that fails every message of the topics in
// `broken` (kafka-go's per-message WriteErrors, as for an undeclared topic) and
// records the event ids it acknowledged, in order, per topic.
type topicWriter struct {
	mu     sync.Mutex
	broken map[string]bool
	sent   map[string][]string
	gate   chan struct{} // when set, the first write waits on it
	calls  int
}

func newTopicWriter(broken ...string) *topicWriter {
	w := &topicWriter{broken: map[string]bool{}, sent: map[string][]string{}}
	for _, b := range broken {
		w.broken[b] = true
	}
	return w
}

func (w *topicWriter) fix(topic string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.broken, topic)
}

func (w *topicWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	w.calls++
	first := w.calls == 1
	gate := w.gate
	w.mu.Unlock()
	if gate != nil && first {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	errs := make(kafka.WriteErrors, len(msgs))
	failed := false
	for i, m := range msgs {
		if w.broken[m.Topic] {
			errs[i] = errors.New("unknown topic or partition")
			failed = true
			continue
		}
		for _, h := range m.Headers {
			if h.Key == "event_id" {
				w.sent[m.Topic] = append(w.sent[m.Topic], string(h.Value))
			}
		}
	}
	if failed {
		return errs
	}
	return nil
}

func (w *topicWriter) sentTo(topic string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.sent[topic]...)
}

func runRelayFor(r *Relay, d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = r.Run(ctx)
}

func fastRelay(store *Store, w relayWriter, batch int) *Relay {
	return &Relay{store: store, writer: w, logger: zap.NewNop(),
		cfg: RelayConfig{PollInterval: 10 * time.Millisecond, BatchSize: batch}}
}

// SL-02: FetchPending's SELECT ... FOR UPDATE SKIP LOCKED ran through pool.Query
// outside a transaction, so its row locks were released when the statement
// ended and claimed nothing: two relays fetched the same rows and both sent
// them (on the release 1 tree: "40 of 40 rows were sent more than once").
func TestPG_TwoRelaysSendEachRowOnce(t *testing.T) {
	pool, store := migratedDB(t)
	const rows = 40
	for i := 0; i < rows; i++ {
		insertMessage(t, pool, store, newID(), time.Now().UTC().Add(time.Duration(i)*time.Millisecond))
	}
	w := &recordingWriter{sends: map[string]int{}, delay: 40 * time.Millisecond}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runRelayFor(fastRelay(store, w, 10), 3*time.Second)
		}()
	}
	wg.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()
	duplicated := 0
	for _, n := range w.sends {
		if n > 1 {
			duplicated++
		}
	}
	assert.Zero(t, duplicated, fmt.Sprintf("%d of %d rows were sent more than once", duplicated, len(w.sends)))
	assert.Len(t, w.sends, rows, "and every row was sent")
	count, _, err := store.PendingStats(context.Background())
	require.NoError(t, err)
	assert.Zero(t, count)
}

// Two relays never interleave a topic: the second finds the topic held and
// leaves it, so rows of one key are never sent out of order by two relays.
func TestPG_TopicIsWorkedByOneRelayAtATimeAndStaysInOrder(t *testing.T) {
	pool, store := migratedDB(t)
	var ids []string
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 20; i++ {
		id := newID()
		ids = append(ids, id)
		insertTopicMessage(t, pool, store, id, "user.events", "same-key", base.Add(time.Duration(i)*time.Second))
	}
	w := newTopicWriter()
	w.gate = make(chan struct{}) // relay A's first write waits here, holding its 10 rows

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	go func() { defer func() { done <- struct{}{} }(); _ = fastRelay(store, w, 10).Run(ctx) }()
	require.Eventually(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.calls == 1 },
		2*time.Second, 5*time.Millisecond, "relay A must be inside its first write")

	// Relay B polls while A holds the topic: it must send nothing.
	bCtx, bCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_ = fastRelay(store, w, 10).Run(bCtx)
	bCancel()
	w.mu.Lock()
	callsWhileHeld := w.calls
	w.mu.Unlock()
	assert.Equal(t, 1, callsWhileHeld, "relay B must not write a row of a topic relay A holds")

	close(w.gate)
	require.Eventually(t, func() bool { return len(w.sentTo("user.events")) == 20 }, 3*time.Second, 10*time.Millisecond)
	cancel()
	<-done

	assert.Equal(t, ids, w.sentTo("user.events"), "the rows of one key went out in the order they were written")
}

// SL-03: one topic that cannot be written must hold back only its own rows.
// On the release 1 tree the oldest rows (the stuck topic's) filled every batch
// and the healthy topic received 0 of its 10 rows.
func TestPG_StuckTopicWaitsInOrderWhileOtherTopicsFlow(t *testing.T) {
	pool, store := migratedDB(t)
	base := time.Now().UTC().Add(-time.Hour)
	var stuckIDs []string
	for i := 0; i < 10; i++ {
		id := newID()
		stuckIDs = append(stuckIDs, id)
		insertTopicMessage(t, pool, store, id, "stuck.events", "k", base.Add(time.Duration(i)*time.Second))
	}
	var healthyIDs []string
	for i := 0; i < 10; i++ {
		id := newID()
		healthyIDs = append(healthyIDs, id)
		insertTopicMessage(t, pool, store, id, "healthy.events", "k", base.Add(time.Minute+time.Duration(i)*time.Second))
	}
	w := newTopicWriter("stuck.events")

	runRelayFor(fastRelay(store, w, 10), time.Second)

	assert.Equal(t, healthyIDs, w.sentTo("healthy.events"), "the healthy topic flows, in order")
	assert.Empty(t, w.sentTo("stuck.events"))
	for _, id := range stuckIDs {
		assert.Equal(t, "pending", statusOf(t, pool, id), "nothing of the stuck topic is skipped or parked")
	}
	stats, err := store.PendingStatsByTopic(context.Background())
	require.NoError(t, err)
	require.Len(t, stats, 1, "only the stuck topic still has a backlog")
	assert.Equal(t, "stuck.events", stats[0].Topic)
	assert.EqualValues(t, 10, stats[0].Pending)
	assert.Greater(t, stats[0].OldestAgeSeconds, 3500.0)

	// The topic is declared: its rows go out, oldest first, none lost.
	w.fix("stuck.events")
	runRelayFor(fastRelay(store, w, 10), time.Second)
	assert.Equal(t, stuckIDs, w.sentTo("stuck.events"))
}

// A failed row holds back the later rows of its own key, even when the broker
// acknowledged them: a later event must never be marked sent past an earlier
// one that was not.
func TestPG_FailedRowHoldsBackLaterRowsOfItsKeyOnly(t *testing.T) {
	pool, store := migratedDB(t)
	base := time.Now().UTC().Add(-time.Hour)
	a1, a2, b1 := newID(), newID(), newID()
	insertTopicMessage(t, pool, store, a1, "user.events", "A", base)
	insertTopicMessage(t, pool, store, b1, "user.events", "B", base.Add(time.Second))
	insertTopicMessage(t, pool, store, a2, "user.events", "A", base.Add(2*time.Second))

	// Row 0 (key A) fails; rows 1 (B) and 2 (A) are acknowledged.
	partial := writerFunc(func(context.Context, ...kafka.Message) error {
		return kafka.WriteErrors{errors.New("leader not available"), nil, nil}
	})
	r := fastRelay(store, partial, 10)
	res := r.poll(context.Background())

	assert.True(t, res.failed)
	assert.Equal(t, 1, res.sent)
	assert.Equal(t, "pending", statusOf(t, pool, a1))
	assert.Equal(t, "sent", statusOf(t, pool, b1), "another key is not held back")
	assert.Equal(t, "pending", statusOf(t, pool, a2), "a2 must not be marked sent past the failed a1")
}

type writerFunc func(context.Context, ...kafka.Message) error

func (f writerFunc) WriteMessages(ctx context.Context, m ...kafka.Message) error { return f(ctx, m...) }

// A crash between the Kafka write and the COMMIT leaves the rows pending: they
// are sent again (the consumer's deduplicator absorbs the repeat) and nothing is
// ever marked sent that the transaction did not commit.
func TestPG_CrashBeforeCommitResendsTheRows(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	id := newID()
	insertMessage(t, pool, store, id, time.Now().UTC())

	var sends int
	killBackend := func(ctx context.Context, msgs []*Message) ([]string, error) {
		sends++
		// The process dies after Kafka acknowledged: simulate it by killing the
		// connection that holds the claim.
		_, err := pool.Exec(ctx, `SELECT pg_terminate_backend(l.pid)
			FROM pg_locks l WHERE l.locktype = 'advisory' AND l.granted AND l.pid <> pg_backend_pid()`)
		require.NoError(t, err)
		return []string{msgs[0].ID}, nil
	}
	_, err := store.ForwardTopic(ctx, "user.events", 10, killBackend)
	require.Error(t, err, "the mark/commit cannot succeed on a dead connection")
	assert.Equal(t, "pending", statusOf(t, pool, id), "nothing was marked sent")

	var again []*Message
	res, err := store.ForwardTopic(ctx, "user.events", 10, forwardAll(&again))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Sent)
	require.Len(t, again, 1, "the row is claimed and sent again")
	assert.Equal(t, 1, sends)
	assert.Equal(t, "sent", statusOf(t, pool, id))
}

func TestPG_ForwardTopicIsBusyWhileAnotherTransactionHoldsTheTopic(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	insertMessage(t, pool, store, newID(), time.Now().UTC())

	inFirst := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = store.ForwardTopic(ctx, "user.events", 10, func(context.Context, []*Message) ([]string, error) {
			close(inFirst)
			<-release
			return nil, nil
		})
	}()
	<-inFirst

	res, err := store.ForwardTopic(ctx, "user.events", 10, func(context.Context, []*Message) ([]string, error) {
		t.Error("the second relay must not send a held topic's rows")
		return nil, nil
	})
	require.NoError(t, err)
	assert.True(t, res.Busy)

	other, err := store.ForwardTopic(ctx, "other.events", 10, forwardAll(new([]*Message)))
	require.NoError(t, err)
	assert.False(t, other.Busy, "another topic is not blocked by the held one")

	close(release)
	<-done
}

func TestPG_MigrationsCreateTheReleaseTwoTables(t *testing.T) {
	url, pool := testpg.New(t)
	require.NoError(t, Migrate(url))
	require.NoError(t, Migrate(url))
	ctx := context.Background()

	for _, table := range []string{"processed_events", "common_idempotency_keys"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_name = $1`, table).Scan(&n))
		assert.Equal(t, 1, n, table)
	}
	for _, idx := range []string{"idx_processed_events_reaper", "idx_common_idempotency_keys_reaper", "idx_outbox_pending_topic"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, idx).Scan(&n))
		assert.Equal(t, 1, n, idx)
	}
	_, err := pool.Exec(ctx, `INSERT INTO common_idempotency_keys (caller, endpoint, key, request_hash, resource_type)
		VALUES ('c', 'e', 'k', '\x00', 'order')`)
	require.Error(t, err, "a resource type without an id violates the both-or-none check")
}

func TestPG_ReaperDeletesOldEventClaimsAndIdempotencyKeys(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO processed_events (consumer, event_id, processed_at) VALUES
			('g', 'old', NOW() - INTERVAL '40 days'), ('g', 'fresh', NOW());
		INSERT INTO common_idempotency_keys (caller, endpoint, key, request_hash, created_at) VALUES
			('c', 'e', 'old', '\x00', NOW() - INTERVAL '2 days'), ('c', 'e', 'fresh', '\x00', NOW());`)
	require.NoError(t, err)

	r := NewReaper(store, zap.NewNop(), ReaperConfig{})
	r.cleanup(ctx)

	var events, keys int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM processed_events`).Scan(&events))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM common_idempotency_keys`).Scan(&keys))
	assert.Equal(t, 1, events, "the 40-day-old claim is reaped, the fresh one kept")
	assert.Equal(t, 1, keys, "the 2-day-old key is reaped (retention 24 h), the fresh one kept")
}

func TestPG_ReaperStopsAskingForATableThatDoesNotExist(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DROP TABLE processed_events; DROP TABLE common_idempotency_keys`)
	require.NoError(t, err)

	r := NewReaper(store, zap.NewNop(), ReaperConfig{})
	r.cleanup(ctx)

	assert.True(t, r.eventsOff)
	assert.True(t, r.idempotencyOff)
}

// R3 against the real store: a topic whose write blocks holds ITS rows (and one
// connection), nothing else. Rows of a healthy topic inserted while it blocks go
// out within a couple of polls.
func TestPG_ABlockedTopicDoesNotDelayAHealthyOne(t *testing.T) {
	pool, store := migratedDB(t)
	for i := 0; i < 3; i++ {
		insertTopicMessage(t, pool, store, newID(), "stuck.events", "k", time.Now().UTC().Add(-time.Minute+time.Duration(i)*time.Millisecond))
	}
	w := newGateWriter()
	release := w.block("stuck.events")
	r := schedulerRelay(store, w, RelayConfig{SendTimeout: time.Minute})
	stop := runInBackground(r)
	defer stop()
	defer close(release)
	require.Eventually(t, func() bool { return w.enteredCount("stuck.events") == 1 }, 3*time.Second, 5*time.Millisecond)

	var healthy []string
	for i := 0; i < 4; i++ {
		id := newID()
		healthy = append(healthy, id)
		insertTopicMessage(t, pool, store, id, "healthy.events", "k", time.Now().UTC())
	}

	require.Eventually(t, func() bool { return w.sentTo("healthy.events") == 4 }, time.Second, 10*time.Millisecond)
	for _, id := range healthy {
		assert.Equal(t, "sent", statusOf(t, pool, id))
	}
	var pending int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM outbox_messages WHERE status = 'pending'`).Scan(&pending))
	assert.Equal(t, 3, pending, "the blocked topic's rows are still pending, claimed by its open transaction")
}
