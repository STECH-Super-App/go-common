package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx is an opaque transaction handle passed by RunInTx to the user's callback.
// Application code threads it through to Publisher.Publish and repository
// WithTx methods without needing to import or reference a concrete
// transaction type. Postgres-backed repositories and the default outbox Store
// type-assert back to pgx.Tx internally; a mismatch is a programmer error.
//
// This exists so domain and application packages can avoid importing
// github.com/jackc/pgx — keeping transaction concerns where they belong (the
// infrastructure layer).
type Tx interface{}

// DBTX abstracts pgx.Tx and *pgxpool.Pool for repository query execution.
// Repository implementations depend on this directly; application code
// should use Tx instead.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxAsPgx recovers the underlying pgx.Tx from an opaque Tx. Intended for
// postgres-specific code paths (repo implementations, the outbox Store)
// that need the concrete pgx methods. Returns the pgx.Tx and true on
// success, or nil and false if tx is nil / not pgx-backed.
//
// Callers that can't proceed without a pgx.Tx should treat the false
// return as a fatal programmer error (wrong wiring, not a runtime
// condition). Most callers in this repo panic on mismatch for that reason.
func TxAsPgx(tx Tx) (pgx.Tx, bool) {
	if tx == nil {
		return nil, false
	}
	pgxTx, ok := tx.(pgx.Tx)
	return pgxTx, ok
}

// RunInTx executes fn within a postgres transaction. The tx passed to fn is
// an opaque outbox.Tx; the concrete pgx.Tx lives inside it, recoverable via
// TxAsPgx by code that needs the raw pgx surface.
//
// If fn returns nil the transaction is committed; otherwise it is rolled
// back. When pool is nil (test scenarios) fn is called once with a nil Tx.
//
// Prefer the TxRunner interface + NewTxRunner for new application-layer code:
// holding a TxRunner lets the application package avoid importing pgxpool at
// all, keeping database concerns in the infrastructure layer.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx Tx) error) error {
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

// TxRunner runs a callback inside a transaction, hiding the concrete pool type
// from application code. Application-layer services should hold a TxRunner
// (not a *pgxpool.Pool) so they don't need to import pgx at all — infrastructure
// owns the pool and hands this capability down.
//
// Satisfied by the pgxpool-backed type returned by NewTxRunner; trivially
// mockable in tests by NewTxRunner(nil) which short-circuits to fn(nil).
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(tx Tx) error) error
}

// pgxTxRunner is the default TxRunner, backed by a *pgxpool.Pool.
type pgxTxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner returns a TxRunner backed by the given pgxpool.Pool. A nil pool
// produces a runner that calls fn once with a nil Tx — the same behaviour as
// RunInTx(ctx, nil, fn), convenient for tests that construct a Service without
// a database.
func NewTxRunner(pool *pgxpool.Pool) TxRunner {
	return &pgxTxRunner{pool: pool}
}

// RunInTx delegates to the package-level RunInTx with the wrapped pool.
func (r *pgxTxRunner) RunInTx(ctx context.Context, fn func(tx Tx) error) error {
	return RunInTx(ctx, r.pool, fn)
}
