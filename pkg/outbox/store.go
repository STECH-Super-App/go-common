package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store manages persistence for outbox_messages rows: the transactional write
// (InsertTx), the relay's claim-send-mark transaction (ForwardTopic), the
// backlog statistics and the reaper's deletes.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new outbox Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// InsertTx writes a message within an existing transaction.
// This is the critical method — the caller's domain write and this insert
// share the same pgx.Tx (wrapped in Tx), guaranteeing atomicity.
func (s *Store) InsertTx(ctx context.Context, tx Tx, msg *Message) error {
	return s.InsertTxLimited(ctx, tx, msg, 0)
}

// InsertTxLimited is InsertTx that refuses a row whose Kafka record would be
// over maxWireBytes (zero or negative: no limit). The size is measured by
// Postgres, on what the relay will actually send: the payload as jsonb::text
// (which is larger than the compact JSON that was handed in — jsonb prints a
// space after every colon and comma), the key, and every header key and value.
// An oversized row is not inserted at all: the statement itself is conditional,
// so even a caller that swallows the error and commits leaves nothing behind.
// The error is the typed 422 ReasonEventTooLarge.
func (s *Store) InsertTxLimited(ctx context.Context, tx Tx, msg *Message, maxWireBytes int) error {
	pgxTx, ok := TxAsPgx(tx)
	if !ok {
		return internalError(ReasonTxInvalid, fmt.Sprintf("outbox: Store.InsertTx expects pgx.Tx, got %T", tx), nil)
	}

	// The explicit casts are needed: a parameter in a SELECT list has no target
	// column to take its type from. `size` is measured once and used for both the
	// condition and nothing else — the row's own columns are unchanged.
	const query = `
		WITH size AS (SELECT octet_length($7::jsonb::text) AS n)
		INSERT INTO outbox_messages
			(id, aggregate_type, aggregate_id, event_type, topic, key, payload, headers, status, created_at)
		SELECT $1::uuid, $2::varchar, $3::varchar, $4::varchar, $5::varchar, $6::varchar,
		       $7::jsonb, $8::jsonb, $9::varchar, $10::timestamptz
		FROM size
		WHERE $12::int <= 0 OR size.n + $11::int <= $12::int
		RETURNING octet_length(payload::text)`

	headersJSON, err := json.Marshal(msg.Headers)
	if err != nil {
		return internalError(ReasonMarshalFailed, "outbox: marshal headers", err)
	}

	overhead := msg.nonPayloadSize()
	var payloadBytes int
	err = pgxTx.QueryRow(ctx, query,
		msg.ID,
		msg.AggregateType,
		msg.AggregateID,
		msg.EventType,
		msg.Topic,
		msg.Key,
		msg.Payload,
		headersJSON,
		string(msg.Status),
		msg.CreatedAt,
		overhead,
		maxWireBytes,
	).Scan(&payloadBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		// Over the limit: nothing was inserted. Ask for the size only to say it.
		var n int
		if qerr := pgxTx.QueryRow(ctx, `SELECT octet_length($1::jsonb::text)`, msg.Payload).Scan(&n); qerr != nil {
			return internalError(ReasonInsertFailed, "outbox: measure oversized message", qerr)
		}
		return eventTooLarge(msg.EventType, n+overhead, maxWireBytes)
	}
	if err != nil {
		return internalError(ReasonInsertFailed, "outbox: insert message", err)
	}

	return nil
}

// SendFunc forwards one topic's claimed rows, oldest first, and returns the ids
// that reached the broker. It is called while the claiming transaction is open:
// ids it returns are marked sent in that same transaction, ids it leaves out
// stay pending. An error does not undo the ids returned with it: a partial
// success is still recorded, and the error is passed back to the caller.
type SendFunc func(ctx context.Context, msgs []*Message) (delivered []string, err error)

// ForwardResult is what ForwardTopic did.
type ForwardResult struct {
	// Claimed is how many pending rows of the topic this call locked.
	Claimed int
	// Sent is how many of them it marked sent.
	Sent int
	// Busy is true when another relay already works the topic: nothing was
	// claimed, and that is not an error.
	Busy bool
}

// pendingTopicsCTE is a loose index scan over idx_outbox_pending_topic: one
// index probe per DISTINCT topic with pending rows (`topic > previous`), never a
// pass over the pending rows themselves. GROUP BY topic would read every pending
// row on each call.
const pendingTopicsCTE = `
	WITH RECURSIVE t(topic) AS (
		(SELECT topic FROM outbox_messages WHERE status = 'pending' ORDER BY topic LIMIT 1)
		UNION ALL
		SELECT (SELECT o.topic FROM outbox_messages o
		        WHERE o.status = 'pending' AND o.topic > t.topic ORDER BY o.topic LIMIT 1)
		FROM t WHERE t.topic IS NOT NULL
	)`

const pendingTopicsQuery = pendingTopicsCTE + `
	SELECT t.topic FROM t
	CROSS JOIN LATERAL (
		SELECT o.created_at FROM outbox_messages o
		WHERE o.status = 'pending' AND o.topic = t.topic
		ORDER BY o.created_at LIMIT 1) oldest
	WHERE t.topic IS NOT NULL
	ORDER BY oldest.created_at, t.topic`

// PendingTopics lists the topics that have pending rows, the one whose oldest
// row is oldest first. The relay walks it once per poll (every second), so it
// must not grow with the backlog.
//
// Big O: O(T log n) for T topics with pending rows — a loose index scan over
// idx_outbox_pending_topic plus one probe per topic for its oldest row; the
// number of pending rows does not enter into it.
func (s *Store) PendingTopics(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, pendingTopicsQuery)
	if err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: list pending topics", err)
	}
	defer rows.Close()

	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			return nil, internalError(ReasonFetchFailed, "outbox: scan pending topic", err)
		}
		topics = append(topics, topic)
	}
	if err := rows.Err(); err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: iterate pending topics", err)
	}
	return topics, nil
}

// ForwardTopic claims up to limit pending rows of ONE topic, hands them to send
// and marks what send delivered, all in a single transaction (SL-02).
//
// Why one transaction. The old FetchPending ran its FOR UPDATE SKIP LOCKED
// outside a transaction, so the row locks ended with the SELECT and claimed
// nothing: two relays (every rolling deploy) read the same rows and both sent
// them. Here the locks live until the COMMIT that marks the rows sent. A relay
// that crashes between the Kafka write and the COMMIT leaves the rows pending
// and they are sent again; the consumer's deduplicator absorbs the repeat.
// Nothing ever marks a row sent that Kafka has not acknowledged.
//
// Why one topic. A topic that cannot be written (not declared, broker refuses)
// must hold back only its own rows, in order. A batch over all topics let one
// such row stall the whole service's outbox (SL-03).
//
// Ordering. A transaction-scoped advisory lock keyed on the topic lets exactly
// one relay work a topic at a time. FOR UPDATE SKIP LOCKED alone would let a
// second relay take the NEXT rows of the same topic while the first still holds
// the earlier ones, and send them first: per-key order would break. Another
// relay on the topic makes this call a no-op (Busy).
//
// Marking is a guarded write: only rows still pending, and the count must match.
func (s *Store) ForwardTopic(ctx context.Context, topic string, limit int, send SendFunc) (ForwardResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ForwardResult{}, internalError(ReasonTxFailed, "outbox: begin forward tx", err)
	}
	// Rollback after a successful Commit is a no-op. It runs on a context that
	// outlives ctx so a cancelled relay still releases the locks, and is bounded
	// so a dead connection cannot hold the relay's shutdown (or the topic's
	// worker) for ever.
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`,
		topicLockKey(topic)).Scan(&acquired); err != nil {
		return ForwardResult{}, internalError(ReasonFetchFailed, "outbox: lock topic "+topic, err)
	}
	if !acquired {
		return ForwardResult{Busy: true}, nil
	}

	msgs, err := claimPending(ctx, tx, topic, limit)
	if err != nil {
		return ForwardResult{}, err
	}
	res := ForwardResult{Claimed: len(msgs)}
	if len(msgs) == 0 {
		return res, nil
	}

	delivered, sendErr := send(ctx, msgs)

	if len(delivered) > 0 {
		const mark = `
			UPDATE outbox_messages
			SET status = 'sent', sent_at = $2
			WHERE id = ANY($1::uuid[]) AND status = 'pending'`
		tag, err := tx.Exec(ctx, mark, delivered, time.Now().UTC())
		if err != nil {
			return res, internalError(ReasonMarkFailed, fmt.Sprintf("outbox: mark sent (%d ids)", len(delivered)), err)
		}
		if tag.RowsAffected() != int64(len(delivered)) {
			// The rows are locked by this transaction, so this is an invariant
			// failure, not a race. Roll back: the rows stay pending and are re-sent.
			return res, internalError(ReasonMarkFailed, fmt.Sprintf(
				"outbox: marked %d of %d delivered rows; rolling back", tag.RowsAffected(), len(delivered)), nil)
		}
		res.Sent = len(delivered)
	}
	if err := tx.Commit(ctx); err != nil {
		res.Sent = 0
		return res, internalError(ReasonTxFailed, "outbox: commit forward tx", err)
	}
	return res, sendErr
}

// rollbackTimeout bounds the rollback of an abandoned forward transaction.
const rollbackTimeout = 5 * time.Second

// topicLockKey is the text the advisory lock of a topic is derived from.
func topicLockKey(topic string) string { return "go-common/outbox/" + topic }

// claimPending locks and reads up to limit pending rows of topic, oldest first.
//
// Big O: O(log n + limit) via the partial index idx_outbox_pending_topic.
func claimPending(ctx context.Context, tx pgx.Tx, topic string, limit int) ([]*Message, error) {
	const query = `
		SELECT id, aggregate_type, aggregate_id, event_type, topic, key,
		       payload, headers, status, created_at, sent_at
		FROM outbox_messages
		WHERE status = 'pending' AND topic = $1
		ORDER BY created_at ASC, id ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, query, topic, limit)
	if err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: claim pending rows of "+topic, err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: iterate claimed rows", err)
	}
	return messages, nil
}

// TopicStat is the pending backlog of one topic.
type TopicStat struct {
	Topic string
	// Pending is the number of pending rows, counted up to TopicBacklogCountCap:
	// a value equal to the cap means "at least this many".
	Pending          int64
	OldestAgeSeconds float64
}

// TopicBacklogCountCap bounds the count PendingStatsByTopic does per topic, so
// the by-topic gauges cost the same on a 600 000-row backlog as on a 20 000-row
// one (measured: 8 ms with 6 topics on a vacuumed 600 000-row backlog; the
// unbounded count read all of it). The age of the oldest row — the alerting signal — is always exact.
const TopicBacklogCountCap = 10_000

const pendingStatsByTopicQuery = pendingTopicsCTE + `
	SELECT t.topic, c.n, EXTRACT(EPOCH FROM (NOW() - c.oldest))::float8
	FROM t
	CROSS JOIN LATERAL (
		SELECT COUNT(*) AS n, MIN(q.created_at) AS oldest FROM (
			SELECT o.created_at FROM outbox_messages o
			WHERE o.status = 'pending' AND o.topic = t.topic
			ORDER BY o.created_at LIMIT $1) q) c
	WHERE t.topic IS NOT NULL
	ORDER BY t.topic`

// PendingStatsByTopic returns the pending count and the age of the oldest
// pending row for every topic that has pending rows. It backs the by-topic
// gauges, so an alert names the stuck topic.
//
// Big O: O(T log n + T·min(p_t, TopicBacklogCountCap)) for T topics with
// pending rows: the topics come from a loose index scan, then each topic reads
// at most TopicBacklogCountCap index entries of idx_outbox_pending_topic, oldest
// first (the first one is the exact oldest age).
func (s *Store) PendingStatsByTopic(ctx context.Context) ([]TopicStat, error) {
	rows, err := s.pool.Query(ctx, pendingStatsByTopicQuery, TopicBacklogCountCap)
	if err != nil {
		return nil, internalError(ReasonStatsFailed, "outbox: pending stats by topic", err)
	}
	defer rows.Close()

	var out []TopicStat
	for rows.Next() {
		var st TopicStat
		if err := rows.Scan(&st.Topic, &st.Pending, &st.OldestAgeSeconds); err != nil {
			return nil, internalError(ReasonStatsFailed, "outbox: scan topic stats", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, internalError(ReasonStatsFailed, "outbox: iterate topic stats", err)
	}
	return out, nil
}

// PendingStats returns the number of pending outbox rows and the age in
// seconds of the oldest one, in a single round trip. It backs the
// outbox_pending_messages and outbox_oldest_pending_age_seconds gauges (§3.3).
//
// There is no `published` column: the schema is status VARCHAR(16) ∈
// {pending,sent} plus sent_at, and the partial index idx_outbox_pending makes
// both aggregates cheap.
//
// MIN(created_at) over zero rows is SQL NULL, so the age is COALESCEd to 0 —
// both numbers must be reportable on an empty table, or the gauges only appear
// once a backlog exists.
//
// Big O: O(p) over the p pending rows via the partial index idx_outbox_pending
// (an index-only count; the oldest age alone would be O(log n)). The sampler
// runs it every OUTBOX_METRICS_INTERVAL (15 s), not on the relay's hot path.
func (s *Store) PendingStats(ctx context.Context) (count int64, oldestAgeSeconds float64, err error) {
	const query = `
		SELECT
			COUNT(*),
			COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))::float8, 0.0)
		FROM outbox_messages
		WHERE status = 'pending'`

	if err := s.pool.QueryRow(ctx, query).Scan(&count, &oldestAgeSeconds); err != nil {
		return 0, 0, internalError(ReasonStatsFailed, "outbox: pending stats", err)
	}

	return count, oldestAgeSeconds, nil
}

// DeleteSent removes messages older than retention that have been successfully sent.
// Returns the count of deleted rows for observability logging.
//
// Big O: O(k log n) where k = deleted rows, via partial index idx_outbox_reaper.
func (s *Store) DeleteSent(ctx context.Context, retention time.Duration) (int64, error) {
	const query = `
		DELETE FROM outbox_messages
		WHERE status = 'sent' AND sent_at < $1`

	cutoff := time.Now().UTC().Add(-retention)

	result, err := s.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, internalError(ReasonCleanupFailed, "outbox: delete sent", err)
	}

	return result.RowsAffected(), nil
}

// pgUndefinedTable is SQLSTATE 42P01.
const pgUndefinedTable = "42P01"

// ErrProcessedTableMissing is DeleteProcessed's answer when the database has no
// processed_outbox_messages table (SQLSTATE 42P01); the Reaper stops asking.
var ErrProcessedTableMissing = errors.New("outbox: processed_outbox_messages does not exist")

// DeleteProcessed removes up to limit deduplicator claims older than retention
// and returns how many it removed; a result equal to limit means more may
// remain. The Reaper calls it in a loop (SL-09).
//
// Selecting the oldest claims by processed_at walks idx_processed_outbox_reaper
// and keeps each DELETE short, so the claim INSERTs of live consumers are never
// queued behind one huge delete. Big O: O(limit log n).
func (s *Store) DeleteProcessed(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	const query = `
		DELETE FROM processed_outbox_messages
		WHERE event_id IN (
			SELECT event_id FROM processed_outbox_messages
			WHERE processed_at < $1
			ORDER BY processed_at
			LIMIT $2)`

	tag, err := s.pool.Exec(ctx, query, time.Now().UTC().Add(-retention), limit)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUndefinedTable {
			return 0, ErrProcessedTableMissing
		}
		return 0, internalError(ReasonCleanupFailed, "outbox: delete processed", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteProcessedEvents removes up to limit claims of the events deduplicator
// (processed_events) older than retention and returns how many it removed.
// Like DeleteProcessed it works in bounded batches, so a live consumer's claim
// INSERT is never queued behind one huge DELETE.
//
// Big O: O(limit log n) via idx_processed_events_reaper.
func (s *Store) DeleteProcessedEvents(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	const query = `
		DELETE FROM processed_events
		WHERE (consumer, event_id) IN (
			SELECT consumer, event_id FROM processed_events
			WHERE processed_at < $1
			ORDER BY processed_at
			LIMIT $2)`

	tag, err := s.pool.Exec(ctx, query, time.Now().UTC().Add(-retention), limit)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUndefinedTable {
			return 0, ErrProcessedTableMissing
		}
		return 0, internalError(ReasonCleanupFailed, "outbox: delete processed events", err)
	}
	return tag.RowsAffected(), nil
}

// ErrIdempotencyTableMissing is DeleteIdempotencyKeys' answer when the database
// has no common_idempotency_keys table (SQLSTATE 42P01); the Reaper stops asking.
var ErrIdempotencyTableMissing = errors.New("outbox: common_idempotency_keys does not exist")

// DeleteIdempotencyKeys removes up to limit idempotency keys older than
// retention (24 h by default) and returns how many it removed.
//
// Big O: O(limit log n) via idx_common_idempotency_keys_reaper.
func (s *Store) DeleteIdempotencyKeys(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	const query = `
		DELETE FROM common_idempotency_keys
		WHERE (caller, endpoint, key) IN (
			SELECT caller, endpoint, key FROM common_idempotency_keys
			WHERE created_at < $1
			ORDER BY created_at
			LIMIT $2)`

	tag, err := s.pool.Exec(ctx, query, time.Now().UTC().Add(-retention), limit)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUndefinedTable {
			return 0, ErrIdempotencyTableMissing
		}
		return 0, internalError(ReasonCleanupFailed, "outbox: delete idempotency keys", err)
	}
	return tag.RowsAffected(), nil
}

// scanMessage maps a single database row to a Message struct.
func scanMessage(rows pgx.Rows) (*Message, error) {
	var (
		msg         Message
		status      string
		headersJSON []byte
	)

	err := rows.Scan(
		&msg.ID,
		&msg.AggregateType,
		&msg.AggregateID,
		&msg.EventType,
		&msg.Topic,
		&msg.Key,
		&msg.Payload,
		&headersJSON,
		&status,
		&msg.CreatedAt,
		&msg.SentAt,
	)
	if err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: scan message", err)
	}

	msg.Status = Status(status)

	if len(headersJSON) > 0 {
		if err := json.Unmarshal(headersJSON, &msg.Headers); err != nil {
			return nil, internalError(ReasonFetchFailed, "outbox: unmarshal headers", err)
		}
	}

	return &msg, nil
}
