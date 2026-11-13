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

// Store manages persistence for outbox_messages rows.
// It separates transactional writes (InsertTx) from non-transactional reads
// (FetchPending) and deletes (DeleteSent).
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
	pgxTx, ok := TxAsPgx(tx)
	if !ok {
		return internalError(ReasonTxInvalid, fmt.Sprintf("outbox: Store.InsertTx expects pgx.Tx, got %T", tx), nil)
	}

	const query = `
		INSERT INTO outbox_messages
			(id, aggregate_type, aggregate_id, event_type, topic, key, payload, headers, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	headersJSON, err := json.Marshal(msg.Headers)
	if err != nil {
		return internalError(ReasonMarshalFailed, "outbox: marshal headers", err)
	}

	_, err = pgxTx.Exec(ctx, query,
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
	)
	if err != nil {
		return internalError(ReasonInsertFailed, "outbox: insert message", err)
	}

	return nil
}

// FetchPending retrieves up to batchSize unsent messages ordered by creation time.
// Uses FOR UPDATE SKIP LOCKED for zero-contention concurrent polling.
//
// Big O: O(log n) via the partial index idx_outbox_pending.
func (s *Store) FetchPending(ctx context.Context, batchSize int) ([]*Message, error) {
	const query = `
		SELECT id, aggregate_type, aggregate_id, event_type, topic, key,
		       payload, headers, status, created_at, sent_at
		FROM outbox_messages
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := s.pool.Query(ctx, query, batchSize)
	if err != nil {
		return nil, internalError(ReasonFetchFailed, "outbox: fetch pending", err)
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
		return nil, internalError(ReasonFetchFailed, "outbox: iterate pending rows", err)
	}

	return messages, nil
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
// Big O: O(log n) via the partial index idx_outbox_pending.
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

// MarkSent updates a message status to 'sent' with a timestamp.
func (s *Store) MarkSent(ctx context.Context, id string) error {
	const query = `
		UPDATE outbox_messages
		SET status = 'sent', sent_at = $2
		WHERE id = $1`

	result, err := s.pool.Exec(ctx, query, id, time.Now().UTC())
	if err != nil {
		return internalError(ReasonMarkFailed, "outbox: mark sent "+id, err)
	}

	if result.RowsAffected() == 0 {
		return internalError(ReasonMarkFailed, "outbox: message "+id+" not found", nil)
	}

	return nil
}

// MarkSentBatch updates many messages to 'sent' in a single UPDATE.
// Used by the relay after a successful batched Kafka write to avoid
// N round-trips. The same sent_at timestamp is applied to every row.
func (s *Store) MarkSentBatch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	const query = `
		UPDATE outbox_messages
		SET status = 'sent', sent_at = $2
		WHERE id = ANY($1::uuid[])`

	if _, err := s.pool.Exec(ctx, query, ids, time.Now().UTC()); err != nil {
		return internalError(ReasonMarkFailed, fmt.Sprintf("outbox: mark sent batch (%d ids)", len(ids)), err)
	}
	return nil
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
