package outbox

// Integration tests against a REAL Postgres (SL-16). Until now no test in
// go-common touched a database, so the SQL of Store, the four migrations and
// the Deduplicator's claim were never executed in CI — SL-02 shipped with a
// comment claiming concurrency safety and a green suite.
//
// They need GOCOMMON_TEST_POSTGRES_URL (see internal/testpg): skipped when it is
// unset, failed when REQUIRE_DB=1 (CI).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/internal/testpg"
)

// migratedDB returns a fresh database with the outbox migrations applied.
func migratedDB(t *testing.T) (*pgxpool.Pool, *Store) {
	t.Helper()
	url, pool := testpg.New(t)
	require.NoError(t, Migrate(url))
	return pool, NewStore(pool)
}

func insertMessage(t *testing.T, pool *pgxpool.Pool, store *Store, id string, createdAt time.Time) {
	t.Helper()
	msg := &Message{
		ID: id, AggregateType: "user", AggregateID: "agg-" + id, EventType: "events.users.v1.UserRegistered",
		Topic: "user.events", Key: "agg-" + id, Payload: []byte(`{"user_id":"` + id + `"}`),
		Headers: map[string]string{"event_id": id, "k": "v"}, Status: StatusPending, CreatedAt: createdAt,
	}
	require.NoError(t, RunInTx(context.Background(), pool, func(tx Tx) error {
		return store.InsertTx(context.Background(), tx, msg)
	}))
}

func newID() string { return uuid.NewString() }

func TestPG_MigrationsApplyIdempotentlyAndCreateTheContract(t *testing.T) {
	url, pool := testpg.New(t)
	ctx := context.Background()

	require.NoError(t, Migrate(url))
	require.NoError(t, Migrate(url), "Migrate must be safe to call on every boot")

	columns := func(table string) map[string]string {
		rows, err := pool.Query(ctx, `SELECT column_name, data_type FROM information_schema.columns
			WHERE table_name = $1`, table)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var name, typ string
			require.NoError(t, rows.Scan(&name, &typ))
			out[name] = typ
		}
		return out
	}

	ob := columns("outbox_messages")
	for _, c := range []string{"id", "aggregate_type", "aggregate_id", "event_type", "topic", "key",
		"payload", "headers", "status", "created_at", "sent_at"} {
		assert.Contains(t, ob, c)
	}
	assert.Equal(t, "uuid", ob["id"])
	assert.Equal(t, "jsonb", ob["payload"])

	pr := columns("processed_outbox_messages")
	assert.Contains(t, pr, "event_id", "migration 004 renamed outbox_id")
	assert.NotContains(t, pr, "outbox_id")
	assert.Contains(t, pr, "processed_at")

	var aggLen int
	require.NoError(t, pool.QueryRow(ctx, `SELECT character_maximum_length FROM information_schema.columns
		WHERE table_name='outbox_messages' AND column_name='aggregate_id'`).Scan(&aggLen))
	assert.Equal(t, 255, aggLen, "migration 003 widened aggregate_id")

	for _, idx := range []string{"idx_outbox_pending", "idx_outbox_reaper", "idx_processed_outbox_reaper"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, idx).Scan(&n))
		assert.Equal(t, 1, n, "index %s", idx)
	}
}

// markSent flips rows to sent the way a relay's transaction does, for tests that
// need sent rows without running one.
func markSent(t *testing.T, pool *pgxpool.Pool, ids ...string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE outbox_messages SET status = 'sent', sent_at = NOW() WHERE id = ANY($1::uuid[])`, ids)
	require.NoError(t, err)
}

// forwardAll is a SendFunc that acknowledges every row, remembering what it saw.
func forwardAll(seen *[]*Message) SendFunc {
	return func(_ context.Context, msgs []*Message) ([]string, error) {
		*seen = append(*seen, msgs...)
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return ids, nil
	}
}

func TestPG_StoreRoundTrip(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	ids := []string{newID(), newID(), newID()}
	for i, id := range ids {
		insertMessage(t, pool, store, id, base.Add(time.Duration(i)*time.Second))
	}

	topics, err := store.PendingTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"user.events"}, topics)

	var got []*Message
	res, err := store.ForwardTopic(ctx, "user.events", 2, func(_ context.Context, msgs []*Message) ([]string, error) {
		got = msgs
		return []string{msgs[0].ID}, nil // only the first is acknowledged
	})
	require.NoError(t, err)
	assert.Equal(t, ForwardResult{Claimed: 2, Sent: 1}, res)
	require.Len(t, got, 2)
	assert.Equal(t, ids[0], got[0].ID, "oldest first")
	assert.Equal(t, ids[1], got[1].ID)
	assert.Equal(t, "user.events", got[0].Topic)
	assert.Equal(t, map[string]string{"event_id": ids[0], "k": "v"}, got[0].Headers)
	assert.JSONEq(t, `{"user_id":"`+ids[0]+`"}`, string(got[0].Payload))
	assert.Equal(t, StatusPending, got[0].Status)
	assert.Nil(t, got[0].SentAt)

	count, age, err := store.PendingStats(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "the unacknowledged row and the unclaimed one stay pending")
	assert.Greater(t, age, 55.0, "age of the oldest pending row in seconds")

	stats, err := store.PendingStatsByTopic(ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, "user.events", stats[0].Topic)
	assert.EqualValues(t, 2, stats[0].Pending)

	var seen []*Message
	res, err = store.ForwardTopic(ctx, "user.events", 10, forwardAll(&seen))
	require.NoError(t, err)
	assert.Equal(t, ForwardResult{Claimed: 2, Sent: 2}, res)
	assert.Equal(t, []string{ids[1], ids[2]}, []string{seen[0].ID, seen[1].ID})

	res, err = store.ForwardTopic(ctx, "user.events", 10, forwardAll(&seen))
	require.NoError(t, err)
	assert.Equal(t, ForwardResult{}, res, "nothing left")
	count, age, err = store.PendingStats(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
	assert.Zero(t, age, "no pending row: the age gauge reads 0, not NULL")
}

// ForwardTopic's mark is a guarded write: a SendFunc that names an id it was
// never handed rolls the whole transaction back, and the rows stay pending.
func TestPG_ForwardTopicRollsBackWhenAnUnclaimedIDIsMarked(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	id := newID()
	insertMessage(t, pool, store, id, time.Now().UTC())

	_, err := store.ForwardTopic(ctx, "user.events", 10, func(_ context.Context, msgs []*Message) ([]string, error) {
		return []string{msgs[0].ID, newID()}, nil
	})

	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "a typed error, got %v", err)
	assert.Equal(t, ReasonMarkFailed, ae.Reason)
	count, _, err := store.PendingStats(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count, "nothing was marked sent")
}

func TestPG_InsertRollsBackWithTheCallersTransaction(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	boom := errors.New("domain write failed")

	err := RunInTx(ctx, pool, func(tx Tx) error {
		require.NoError(t, store.InsertTx(ctx, tx, &Message{
			ID: newID(), AggregateType: "a", AggregateID: "b", EventType: "e", Topic: "t",
			Payload: []byte(`{}`), Headers: map[string]string{}, Status: StatusPending, CreatedAt: time.Now(),
		}))
		return boom
	})

	require.ErrorIs(t, err, boom)
	count, _, err := store.PendingStats(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count, "the outbox row must vanish with the domain write")
}

func TestPG_DeleteSentHonoursRetention(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	oldID, freshID, pendingID := newID(), newID(), newID()
	for _, id := range []string{oldID, freshID, pendingID} {
		insertMessage(t, pool, store, id, time.Now().UTC().Add(-time.Hour))
	}
	markSent(t, pool, oldID, freshID)
	_, err := pool.Exec(ctx, `UPDATE outbox_messages SET sent_at = NOW() - INTERVAL '5 days' WHERE id = $1`, oldID)
	require.NoError(t, err)

	n, err := store.DeleteSent(ctx, 72*time.Hour)

	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "only the sent row older than the retention")
	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages`).Scan(&left))
	assert.Equal(t, 2, left)
}

func seedClaims(t *testing.T, pool *pgxpool.Pool, old, fresh int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < old; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO processed_outbox_messages (event_id, processed_at)
			VALUES ($1, NOW() - INTERVAL '40 days')`, newID())
		require.NoError(t, err)
	}
	for i := 0; i < fresh; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO processed_outbox_messages (event_id, processed_at)
			VALUES ($1, NOW())`, newID())
		require.NoError(t, err)
	}
}

func countClaims(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM processed_outbox_messages`).Scan(&n))
	return n
}

// SL-09: nothing ever deleted a processed claim.
func TestPG_DeleteProcessedWorksInBoundedBatches(t *testing.T) {
	pool, store := migratedDB(t)
	seedClaims(t, pool, 25, 5)

	var batches []int64
	for {
		n, err := store.DeleteProcessed(context.Background(), 30*24*time.Hour, 10)
		require.NoError(t, err)
		batches = append(batches, n)
		if n < 10 {
			break
		}
	}

	assert.Equal(t, []int64{10, 10, 5}, batches, "each statement is bounded by the limit")
	assert.Equal(t, 5, countClaims(t, pool), "claims inside the retention survive")
}

func TestPG_ReaperCleansSentRowsAndProcessedClaimsOnARealStore(t *testing.T) {
	pool, store := migratedDB(t)
	seedClaims(t, pool, 23, 2)
	r := NewReaper(store, zap.NewNop(), ReaperConfig{
		Retention: time.Hour, ProcessedRetention: 30 * 24 * time.Hour, ProcessedBatchSize: 10,
	})

	r.cleanup(context.Background())

	assert.Equal(t, 2, countClaims(t, pool))
}

func TestPG_DeleteProcessedOnAMissingTableIsTheSentinel(t *testing.T) {
	pool, store := migratedDB(t)
	_, err := pool.Exec(context.Background(), `DROP TABLE processed_outbox_messages`)
	require.NoError(t, err)

	_, err = store.DeleteProcessed(context.Background(), time.Hour, 10)

	require.ErrorIs(t, err, ErrProcessedTableMissing)
}

func TestPG_DeduplicatorRunsAnEventOnce(t *testing.T) {
	pool, _ := migratedDB(t)
	d := NewDeduplicator(pool)
	ctx := context.Background()
	id := newID()

	var runs int
	for i := 0; i < 3; i++ {
		require.NoError(t, d.Process(ctx, id, func() error { runs++; return nil }))
	}

	assert.Equal(t, 1, runs, "a redelivery is skipped")
	assert.Equal(t, 1, countClaims(t, pool))
}

func TestPG_DeduplicatorConcurrentClaimsRunTheHandlerOnce(t *testing.T) {
	pool, _ := migratedDB(t)
	d := NewDeduplicator(pool)
	id := newID()

	var runs atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			assert.NoError(t, d.Process(context.Background(), id, func() error {
				runs.Add(1)
				time.Sleep(50 * time.Millisecond) // hold the claim so the others collide with it
				return nil
			}))
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, runs.Load(), "the claim insert is the compare-and-set: one deliverer wins")
}

func TestPG_DeduplicatorHandlerErrorRollsTheClaimBack(t *testing.T) {
	pool, _ := migratedDB(t)
	d := NewDeduplicator(pool)
	ctx := context.Background()
	id := newID()
	boom := errors.New("handler failed")

	err := d.Process(ctx, id, func() error { return boom })
	require.Same(t, boom, err, "the handler's error reaches the caller untouched (ErrPoisonPill must survive)")
	assert.Zero(t, countClaims(t, pool), "a failed handler must not leave its claim")

	var ran bool
	require.NoError(t, d.Process(ctx, id, func() error { ran = true; return nil }))
	assert.True(t, ran, "the retry runs")
}

func TestPG_DeduplicatorCleanupProcessed(t *testing.T) {
	pool, _ := migratedDB(t)
	seedClaims(t, pool, 4, 3)

	n, err := NewDeduplicator(pool).CleanupProcessed(context.Background(), 30*24*time.Hour)

	require.NoError(t, err)
	assert.EqualValues(t, 4, n)
	assert.Equal(t, 3, countClaims(t, pool))
}

// recordingWriter counts how often each event id was written. (Release 1 kept
// the SL-02 test as known-red behind GOCOMMON_KNOWN_RED=1; release 2 fixed it and
// the test, TestPG_TwoRelaysSendEachRowOnce, runs always.)
type recordingWriter struct {
	mu    sync.Mutex
	sends map[string]int
	delay time.Duration
}

func (w *recordingWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	select {
	case <-time.After(w.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range msgs {
		for _, h := range m.Headers {
			if h.Key == "event_id" {
				w.sends[string(h.Value)]++
			}
		}
	}
	return nil
}
