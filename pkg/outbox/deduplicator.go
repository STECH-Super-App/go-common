package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deduplicator tracks processed outbox message IDs to guarantee idempotent
// consumption. The outbox pattern provides at-least-once delivery, which
// means consumers may receive the same message more than once. This component
// solves that by recording each processed outbox_id in a Postgres table.
//
// Usage in a consumer handler:
//
//	func (h *Handler) Handle(ctx context.Context, outboxID string, payload []byte) error {
//	    return h.dedup.Process(ctx, outboxID, func() error {
//	        return h.doActualWork(ctx, payload)
//	    })
//	}
type Deduplicator struct {
	pool *pgxpool.Pool
}

// NewDeduplicator creates a new idempotency guard backed by the
// processed_outbox_messages table.
func NewDeduplicator(pool *pgxpool.Pool) *Deduplicator {
	return &Deduplicator{pool: pool}
}

// Process executes fn only if outboxID has not been processed before.
// The check and the insert are done inside a transaction, so concurrent
// deliveries of the same message are safely serialized.
//
// Returns nil without calling fn if the message was already processed.
func (d *Deduplicator) Process(ctx context.Context, outboxID string, fn func() error) error {
	if outboxID == "" {
		// No outbox_id header — skip dedup, just process.
		return fn()
	}

	return RunInTx(ctx, d.pool, func(tx Tx) error {
		pgxTx, ok := TxAsPgx(tx)
		if !ok {
			return fmt.Errorf("outbox dedup: expected pgx.Tx, got %T", tx)
		}
		alreadyProcessed, err := d.exists(ctx, pgxTx, outboxID)
		if err != nil {
			return err
		}

		if alreadyProcessed {
			return nil
		}

		if err := fn(); err != nil {
			return err
		}

		return d.record(ctx, pgxTx, outboxID)
	})
}

// exists checks whether an outbox_id has already been processed.
// Uses SELECT ... FOR UPDATE to serialize concurrent checks for the same ID.
func (d *Deduplicator) exists(ctx context.Context, tx pgx.Tx, outboxID string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM processed_outbox_messages WHERE outbox_id = $1 FOR UPDATE)`

	var found bool
	if err := tx.QueryRow(ctx, query, outboxID).Scan(&found); err != nil {
		return false, fmt.Errorf("outbox dedup: check exists: %w", err)
	}

	return found, nil
}

// record inserts a processed outbox_id.
func (d *Deduplicator) record(ctx context.Context, tx pgx.Tx, outboxID string) error {
	const query = `INSERT INTO processed_outbox_messages (outbox_id, processed_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`

	_, err := tx.Exec(ctx, query, outboxID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("outbox dedup: record processed: %w", err)
	}

	return nil
}

// CleanupProcessed deletes records older than retention to prevent table bloat.
// Called automatically by the Reaper if the table exists.
func (d *Deduplicator) CleanupProcessed(ctx context.Context, retention time.Duration) (int64, error) {
	const query = `DELETE FROM processed_outbox_messages WHERE processed_at < $1`

	cutoff := time.Now().UTC().Add(-retention)
	result, err := d.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("outbox dedup: cleanup: %w", err)
	}

	return result.RowsAffected(), nil
}
