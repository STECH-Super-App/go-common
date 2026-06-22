package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deduplicator tracks processed event IDs to guarantee idempotent consumption.
// The outbox pattern provides at-least-once delivery, which means consumers
// may receive the same message more than once. This component solves that by
// recording each processed event_id in a Postgres table.
//
// Usage in a consumer handler:
//
//	func (h *Handler) Handle(ctx context.Context, eventID string, payload []byte) error {
//	    return h.dedup.Process(ctx, eventID, func() error {
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

// Process executes fn at most once per eventID, even across concurrent
// deliveries on different pods.
//
// The claim is taken by inserting the event_id FIRST (INSERT ... ON CONFLICT
// DO NOTHING) and branching on whether the row was actually taken:
//
//   - row taken (RowsAffected == 1) → this delivery owns the event; fn runs
//     inside the same transaction and is committed atomically with the claim.
//   - conflict (RowsAffected == 0) → the event was already processed (or is
//     being processed by the deliverer that holds the row lock); fn is skipped
//     and nil is returned.
//
// Inserting first is what makes the claim atomic. The previous implementation
// did SELECT ... FOR UPDATE on a not-yet-existing row, which locks nothing at
// READ COMMITTED — two concurrent first-time deliveries both passed the check
// and both ran fn (double side effects). With the insert taken under the
// primary-key constraint, only one concurrent deliverer can win the row, so fn
// runs exactly once.
//
// Returns nil without calling fn if the message was already processed.
func (d *Deduplicator) Process(ctx context.Context, eventID string, fn func() error) error {
	if eventID == "" {
		// No event_id header — skip dedup, just process.
		return fn()
	}

	return RunInTx(ctx, d.pool, func(tx Tx) error {
		pgxTx, ok := TxAsPgx(tx)
		if !ok {
			return fmt.Errorf("outbox dedup: expected pgx.Tx, got %T", tx)
		}
		return d.processInTx(ctx, pgxTx, eventID, fn)
	})
}

// processInTx is the claim-and-run logic for a single transaction, split out
// from Process so the exactly-once branch can be unit-tested with a fake
// pgx.Tx (no pool / database required).
func (d *Deduplicator) processInTx(ctx context.Context, tx pgx.Tx, eventID string, fn func() error) error {
	claimed, err := d.claim(ctx, tx, eventID)
	if err != nil {
		return err
	}

	// Lost the claim: another deliverer already owns this event_id.
	// Skip the handler — this is the already-processed path.
	if !claimed {
		return nil
	}

	// Won the claim: run the handler in the same tx so the side effects
	// and the recorded event_id commit (or roll back) together.
	return fn()
}

// claim attempts to take ownership of an event_id by inserting it. It returns
// true if this transaction took the row (first-time delivery, fn should run)
// and false if the row already existed (already processed, fn should be
// skipped).
//
// The INSERT ... ON CONFLICT DO NOTHING is the atomic compare-and-set: the
// primary-key constraint on event_id lets exactly one concurrent deliverer
// take the row. RowsAffected reports whether this statement is the one that
// took it.
func (d *Deduplicator) claim(ctx context.Context, tx pgx.Tx, eventID string) (bool, error) {
	const query = `INSERT INTO processed_outbox_messages (event_id, processed_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`

	tag, err := tx.Exec(ctx, query, eventID, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("outbox dedup: claim event: %w", err)
	}

	return tag.RowsAffected() == 1, nil
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
