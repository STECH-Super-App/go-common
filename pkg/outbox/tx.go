package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX abstracts pgx.Tx and *pgxpool.Pool so that repositories can accept
// either for query execution. This enables the WithTx() pattern where a
// repository can be bound to a transaction for outbox-style atomic writes.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RunInTx executes fn within a database transaction.
// If fn returns nil the transaction is committed; otherwise it is rolled back.
// The caller should use the pgx.Tx passed to fn for all database operations
// that must be atomic (including outbox.Publisher.Publish).
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	if pool == nil {
		return fn(nil)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin tx: %w", err)
	}

	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox: commit tx: %w", err)
	}

	return nil
}
