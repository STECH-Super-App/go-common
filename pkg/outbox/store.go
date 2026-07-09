package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store manages persistence for outbox_messages rows.
// It separates transactional writes (InsertTx) from non-transactional claims
// (ClaimPending), releases (ReleaseBatch, ReleaseStuck) and deletes (DeleteSent).
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
		return fmt.Errorf("outbox: Store.InsertTx expects pgx.Tx, got %T", tx)
	}

	const query = `
		INSERT INTO outbox_messages
			(id, aggregate_type, aggregate_id, event_type, topic, key, payload, headers, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	headersJSON, err := json.Marshal(msg.Headers)
	if err != nil {
		return fmt.Errorf("outbox: marshal headers: %w", err)
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
		return fmt.Errorf("outbox: insert message: %w", err)
	}

	return nil
}

// ClaimPending atomically claims up to batchSize pending messages for this
// relay by flipping them to 'processing' and stamping claimed_at, returning
// the claimed rows. FOR UPDATE SKIP LOCKED in the inner select keeps
// concurrent relays contention-free, and — unlike the previous standalone
// autocommit SELECT, whose row locks evaporated when the statement returned —
// the status flip persists the claim, so no other relay can pick up the same
// rows. The caller must terminate every claim: MarkSentBatch on success,
// ReleaseBatch on failure (the Reaper's ReleaseStuck backstops crashes).
//
// RETURNING order is unspecified in Postgres, so results are re-sorted by
// created_at in Go. Big O: O(log n) claim via the partial index
// idx_outbox_pending + O(k log k) sort for k = batch size.
func (s *Store) ClaimPending(ctx context.Context, batchSize int) ([]*Message, error) {
	const query = `
		UPDATE outbox_messages
		SET status = 'processing', claimed_at = $2
		FROM (
			SELECT id
			FROM outbox_messages
			WHERE status = 'pending'
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		) AS claimable
		WHERE outbox_messages.id = claimable.id
		RETURNING outbox_messages.id, outbox_messages.aggregate_type,
		          outbox_messages.aggregate_id, outbox_messages.event_type,
		          outbox_messages.topic, outbox_messages.key,
		          outbox_messages.payload, outbox_messages.headers,
		          outbox_messages.status, outbox_messages.created_at,
		          outbox_messages.sent_at`

	rows, err := s.pool.Query(ctx, query, batchSize, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("outbox: claim pending: %w", err)
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
		return nil, fmt.Errorf("outbox: iterate claimed rows: %w", err)
	}

	sortMessagesByCreatedAt(messages)
	return messages, nil
}

// ReleaseBatch returns claimed messages to 'pending' so the next poll retries
// them immediately (preserving the relay's ~PollInterval retry latency after
// a failed Kafka write). The status guard keeps the release idempotent and
// scoped: rows already released (or marked sent) are untouched.
func (s *Store) ReleaseBatch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	const query = `
		UPDATE outbox_messages
		SET status = 'pending', claimed_at = NULL
		WHERE id = ANY($1::uuid[]) AND status = 'processing'`

	if _, err := s.pool.Exec(ctx, query, ids); err != nil {
		return fmt.Errorf("outbox: release batch (%d ids): %w", len(ids), err)
	}
	return nil
}

// ReleaseStuck returns messages that have been in 'processing' longer than
// olderThan back to 'pending'. This is the Reaper's backstop for claims
// orphaned by a relay that crashed between ClaimPending and MarkSentBatch/
// ReleaseBatch — without it those rows would never be forwarded. Returns the
// count of released rows for observability logging.
//
// Big O: O(k log n) where k = released rows, via partial index idx_outbox_processing.
func (s *Store) ReleaseStuck(ctx context.Context, olderThan time.Duration) (int64, error) {
	const query = `
		UPDATE outbox_messages
		SET status = 'pending', claimed_at = NULL
		WHERE status = 'processing' AND claimed_at < $1`

	cutoff := time.Now().UTC().Add(-olderThan)

	result, err := s.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("outbox: release stuck: %w", err)
	}

	return result.RowsAffected(), nil
}

// MarkSent updates a message status to 'sent' with a timestamp.
func (s *Store) MarkSent(ctx context.Context, id string) error {
	const query = `
		UPDATE outbox_messages
		SET status = 'sent', sent_at = $2, claimed_at = NULL
		WHERE id = $1`

	result, err := s.pool.Exec(ctx, query, id, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("outbox: mark sent %s: %w", id, err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("outbox: message %s not found", id)
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
		SET status = 'sent', sent_at = $2, claimed_at = NULL
		WHERE id = ANY($1::uuid[])`

	if _, err := s.pool.Exec(ctx, query, ids, time.Now().UTC()); err != nil {
		return fmt.Errorf("outbox: mark sent batch (%d ids): %w", len(ids), err)
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
		return 0, fmt.Errorf("outbox: delete sent: %w", err)
	}

	return result.RowsAffected(), nil
}

// sortMessagesByCreatedAt restores creation order after a RETURNING clause,
// whose row order Postgres leaves unspecified. Stable so messages sharing a
// created_at timestamp keep their scan order.
func sortMessagesByCreatedAt(messages []*Message) {
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
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
		return nil, fmt.Errorf("outbox: scan message: %w", err)
	}

	msg.Status = Status(status)

	if len(headersJSON) > 0 {
		if err := json.Unmarshal(headersJSON, &msg.Headers); err != nil {
			return nil, fmt.Errorf("outbox: unmarshal headers: %w", err)
		}
	}

	return &msg, nil
}
