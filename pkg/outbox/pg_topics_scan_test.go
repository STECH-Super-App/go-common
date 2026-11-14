package outbox

// The two reads the relay and the sampler repeat on a schedule must not grow with
// the backlog (R9). pg_stat_xact_user_indexes counts index entries read inside
// the transaction, so it is an exact meter.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedBacklog inserts n pending rows over the given topics, oldest topic first.
func seedBacklog(t *testing.T, pool *pgxpool.Pool, topics []string, n int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, event_type, topic, key, payload, created_at)
		SELECT 'a', i::text, 'e', ($1::text[])[1 + (i % $2::int)], i::text, '{}', NOW() - ((n - i) || ' ms')::interval
		FROM (SELECT i, $3::int AS n FROM generate_series(1, $3::int) i) g`, topics, len(topics), n)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `VACUUM ANALYZE outbox_messages`)
	require.NoError(t, err)
}

// buffersOf EXPLAINs a query with ANALYZE and returns the shared buffers it
// touched (hit + read): a deterministic measure of how much it reads.
func buffersOf(t *testing.T, pool *pgxpool.Pool, query string, args ...any) (blocks int64, plan string) {
	t.Helper()
	var raw []byte
	require.NoError(t, pool.QueryRow(context.Background(),
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query, args...).Scan(&raw))
	var doc []struct {
		Plan struct {
			Hit  int64 `json:"Shared Hit Blocks"`
			Read int64 `json:"Shared Read Blocks"`
		} `json:"Plan"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc, 1)
	return doc[0].Plan.Hit + doc[0].Plan.Read, string(raw)
}

func TestPG_PendingTopicsCostDoesNotGrowWithTheBacklog(t *testing.T) {
	topics := []string{"a.events", "b.events", "c.events", "d.events"}
	var small, large, grouped int64
	for _, tc := range []struct {
		rows int
		into *int64
	}{{2_000, &small}, {60_000, &large}} {
		pool, _ := migratedDB(t)
		seedBacklog(t, pool, topics, tc.rows)
		blocks, plan := buffersOf(t, pool, pendingTopicsQuery)
		assert.NotContains(t, plan, "Seq Scan", "no sequential scan")
		*tc.into = blocks
		if tc.rows == 60_000 {
			// The release-2 query, for scale: it reads every pending row.
			grouped, _ = buffersOf(t, pool, `SELECT topic FROM outbox_messages WHERE status = 'pending'
				GROUP BY topic ORDER BY MIN(created_at), topic`)
		}
	}
	assert.Less(t, large, int64(150), "a loose index scan touches a few buffers per topic")
	assert.InDelta(t, small, large, 60, "30x the backlog, about the same reads (%d vs %d)", small, large)
	assert.Greater(t, grouped, 20*large, "the GROUP BY it replaces read %d buffers", grouped)
}

func TestPG_PendingStatsByTopicIsBoundedByTheCap(t *testing.T) {
	pool, store := migratedDB(t)
	seedBacklog(t, pool, []string{"a.events", "b.events"}, 4*TopicBacklogCountCap)

	stats, err := store.PendingStatsByTopic(context.Background())
	require.NoError(t, err)
	require.Len(t, stats, 2)
	for _, st := range stats {
		assert.EqualValues(t, TopicBacklogCountCap, st.Pending, "the count saturates at the cap: %d rows are pending per topic", 2*TopicBacklogCountCap)
		assert.Greater(t, st.OldestAgeSeconds, 0.0, "the oldest age is exact however large the backlog")
	}
	blocks, plan := buffersOf(t, pool, pendingStatsByTopicQuery, TopicBacklogCountCap)
	assert.NotContains(t, plan, "Seq Scan")
	all, _ := buffersOf(t, pool, `SELECT topic, COUNT(*) FROM outbox_messages WHERE status = 'pending' GROUP BY topic`)
	assert.Less(t, blocks, all/2, "it reads at most the cap per topic (%d buffers), not the whole backlog (%d)", blocks, all)

	// A small backlog is counted exactly.
	_, err = pool.Exec(context.Background(), `DELETE FROM outbox_messages`)
	require.NoError(t, err)
	seedBacklog(t, pool, []string{"c.events"}, 37)
	exact, err := store.PendingStatsByTopic(context.Background())
	require.NoError(t, err)
	require.Len(t, exact, 1)
	assert.EqualValues(t, 37, exact[0].Pending)
}

// Topics come out oldest-head first, whatever the loose scan's alphabetical order.
func TestPG_PendingTopicsOrderByTheirOldestRow(t *testing.T) {
	pool, store := migratedDB(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i, topic := range []string{"z.events", "m.events", "a.events"} { // z is oldest, a is newest
		insertTopicMessage(t, pool, store, newID(), topic, "k", base.Add(time.Duration(i)*time.Minute))
		insertTopicMessage(t, pool, store, newID(), topic, "k", base.Add(time.Duration(i)*time.Minute+time.Second))
	}
	insertTopicMessage(t, pool, store, newID(), "sent.events", "k", base.Add(-time.Hour))
	markSent(t, pool, func() string {
		var id string
		require.NoError(t, pool.QueryRow(context.Background(), `SELECT id FROM outbox_messages WHERE topic = 'sent.events'`).Scan(&id))
		return id
	}())

	got, err := store.PendingTopics(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"z.events", "m.events", "a.events"}, got, "oldest head first; a topic with only sent rows is absent")

	empty, _ := migratedDB(t)
	_ = empty
	none, err := NewStore(empty).PendingTopics(context.Background())
	require.NoError(t, err)
	assert.Empty(t, none)
}
