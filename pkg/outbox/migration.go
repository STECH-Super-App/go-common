package outbox

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationsFS strips the "migrations/" prefix so goose sees .sql files at root.
var migrationsFS, _ = fs.Sub(migrations, "migrations")

// outboxGooseTable is the dedicated goose version table for outbox migrations.
// Using a separate table prevents collision with service-specific migrations
// that share the same database.
const outboxGooseTable = "goose_db_version_outbox"

// migrateLockKey names the advisory lock that serialises outbox.Migrate across
// pods (hashed by Postgres to the lock's bigint).
const migrateLockKey = "go-common/outbox/migrate"

// migrateTimeout bounds one Migrate call, the wait for another pod's run
// included. The slowest step is the concurrent index build of migration 007 on
// a large outbox_messages.
const migrateTimeout = 30 * time.Minute

// migrateLockPoll is how often a pod that did not get the lock asks again.
const migrateLockPoll = 200 * time.Millisecond

// concurrentIndexes are the indexes a migration builds CONCURRENTLY. An
// interrupted build leaves one INVALID, which `IF NOT EXISTS` would then keep
// for ever: Migrate drops it before the build is tried again.
var concurrentIndexes = []string{"idx_outbox_pending_topic"}

// Migrate runs the outbox table migration against the given Postgres database.
// Safe to call repeatedly — goose tracks which migrations have already been applied.
//
// Call it on every boot, before the service's consumers start. Its position
// relative to the service's own migrations does not matter: every statement
// that could meet an object the service's migration created is `IF NOT EXISTS`
// (chat-service runs it first, auth-service before its own, others after).
//
// Pods that start together are serialised by a Postgres advisory lock: the
// first applies the migrations, the others wait and find them applied. A pod
// that waits holds one connection and no transaction.
//
//	if err := outbox.Migrate(cfg.Postgres.URL); err != nil {
//	    zlog.Fatal("outbox migration failed", zap.Error(err))
//	}
func Migrate(postgresURL string) error {
	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		return internalError(ReasonMigrateFailed, "outbox: open db for migration", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	// The lock lives on this connection's session; goose uses others from the pool.
	lockConn, err := db.Conn(ctx)
	if err != nil {
		return internalError(ReasonMigrateFailed, "outbox: connect for migration lock", err)
	}
	defer func() { _ = lockConn.Close() }()
	if err := acquireMigrateLock(ctx, lockConn); err != nil {
		return err
	}
	defer func() {
		// Best effort: closing the connection releases the lock as well.
		_, _ = lockConn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, migrateLockKey)
	}()

	if err := dropInvalidIndexes(ctx, lockConn); err != nil {
		return err
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrationsFS,
		goose.WithTableName(outboxGooseTable),
	)
	if err != nil {
		return internalError(ReasonMigrateFailed, "outbox: create migration provider", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return internalError(ReasonMigrateFailed, "outbox: run migrations", err)
	}

	return nil
}

// acquireMigrateLock polls pg_try_advisory_lock instead of blocking in
// pg_advisory_lock. A blocked statement is an open transaction, and migration
// 007's CREATE INDEX CONCURRENTLY waits for every open transaction to end: the
// lock holder would wait for the waiters and the waiters for the holder.
func acquireMigrateLock(ctx context.Context, conn *sql.Conn) error {
	for {
		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, migrateLockKey).Scan(&got); err != nil {
			return internalError(ReasonMigrateFailed, "outbox: take the migration lock", err)
		}
		if got {
			return nil
		}
		select {
		case <-ctx.Done():
			return internalError(ReasonMigrateFailed, "outbox: waiting for the migration lock", ctx.Err())
		case <-time.After(migrateLockPoll):
		}
	}
}

// dropInvalidIndexes removes an INVALID leftover of a concurrent index build, so
// the migration that builds it again does not skip it. Runs under the migration
// lock, so no other pod of this library is building it.
func dropInvalidIndexes(ctx context.Context, conn *sql.Conn) error {
	for _, name := range concurrentIndexes {
		var invalid bool
		err := conn.QueryRowContext(ctx, `
			SELECT NOT i.indisvalid
			FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.oid = to_regclass($1)`, name).Scan(&invalid)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return internalError(ReasonMigrateFailed, "outbox: look for an invalid index "+name, err)
		}
		if !invalid {
			continue
		}
		// DROP INDEX CONCURRENTLY cannot run inside a transaction or a function.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`DROP INDEX CONCURRENTLY IF EXISTS %s`, name)); err != nil {
			return internalError(ReasonMigrateFailed, "outbox: drop the invalid index "+name, err)
		}
	}
	return nil
}
