package outbox

// Migrate against a REAL Postgres: name collisions with a service's own tables
// (R1), either order with a service's own outbox migration, two pods booting
// together and the concurrent build of migration 007 (R8).

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/idempotency"
	"github.com/STECH-Super-App/go-common/pkg/internal/testpg"
)

// migrateUpTo applies go-common's migrations 1..version and no further: the
// state of a database whose service runs an older go-common.
func migrateUpTo(t *testing.T, url string, version int64) {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS, goose.WithTableName(outboxGooseTable))
	require.NoError(t, err)
	_, err = p.UpTo(context.Background(), version)
	require.NoError(t, err)
}

// The three shapes of an `idempotency_keys` table that services already own
// (order-service migrations/00001_init.sql + 00008, seo-service
// 00003_ops_core.sql, machinery-service's runtime EnsureSchema). The library
// must not touch, adopt or fail on any of them.
var foreignIdempotencyTables = map[string]string{
	"order": `CREATE TABLE idempotency_keys (
		endpoint text NOT NULL, tenant_key text NOT NULL, idem_key text NOT NULL, request_hash text NOT NULL,
		state text NOT NULL DEFAULT 'in_progress', response_status int, response_body jsonb,
		created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
		PRIMARY KEY (endpoint, tenant_key, idem_key));
		CREATE INDEX idempotency_keys_expiry ON idempotency_keys (expires_at)`,
	"seo": `CREATE TABLE idempotency_keys (
		actor uuid NOT NULL, method text NOT NULL, route text NOT NULL, idem_key text NOT NULL,
		request_hash text NOT NULL, state text NOT NULL DEFAULT 'in_progress', response_status integer,
		response_body bytea, response_location text, created_at timestamptz NOT NULL DEFAULT now(),
		expires_at timestamptz NOT NULL,
		CONSTRAINT idempotency_keys_pkey PRIMARY KEY (actor, method, route, idem_key));
		CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at)`,
	// No created_at: migration 006 of release 2 failed on its index here, and
	// machinery-service crash-looped on every existing database.
	"machinery": `CREATE TABLE idempotency_keys (
		user_id TEXT NOT NULL, key TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL,
		status INTEGER NOT NULL, body BYTEA NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (user_id, key, method, path));
		CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at)`,
}

// R1: a service that already owns an idempotency_keys table keeps it, untouched,
// and the library's own table sits beside it under another name.
func TestPG_MigrateCoexistsWithAServicesOwnIdempotencyKeysTable(t *testing.T) {
	for name, ddl := range foreignIdempotencyTables {
		t.Run(name, func(t *testing.T) {
			url, pool := testpg.New(t)
			ctx := context.Background()
			migrateUpTo(t, url, 5)
			_, err := pool.Exec(ctx, ddl)
			require.NoError(t, err)
			_, err = pool.Exec(ctx, `SELECT * FROM idempotency_keys LIMIT 0`)
			require.NoError(t, err)

			require.NoError(t, Migrate(url), "outbox.Migrate must not collide with the service's table")

			// The service's table is exactly as it was: no library column, no library row.
			var cols int
			require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM information_schema.columns
				WHERE table_name = 'idempotency_keys' AND column_name IN ('caller', 'resource_type', 'resource_id')`).Scan(&cols))
			assert.Zero(t, cols, "the library must not add columns to a service's idempotency_keys")

			// The library's table exists under its own name, and a claim works on it.
			var n int
			require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM information_schema.tables
				WHERE table_name = 'common_idempotency_keys'`).Scan(&n))
			assert.Equal(t, 1, n)

			key, err := idempotency.ParseKey("k-1")
			require.NoError(t, err)
			claim, err := idempotency.NewClaim(key, idempotency.Request{Caller: "u1", Endpoint: "POST /x", Target: "POST /x", Body: []byte("{}")})
			require.NoError(t, err)
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			out, err := claim.Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
				return idempotency.Resource{Type: "order", ID: "1"}, nil
			})
			require.NoError(t, err)
			require.NoError(t, tx.Commit(ctx))
			assert.False(t, out.Replayed)

			tx, err = pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			out, err = claim.Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
				t.Error("work must not run on a replay")
				return idempotency.Resource{}, nil
			})
			require.NoError(t, err)
			require.NoError(t, tx.Commit(ctx))
			assert.True(t, out.Replayed)

			// The reaper works on the library's table and leaves the foreign one alone.
			_, err = pool.Exec(ctx, `UPDATE common_idempotency_keys SET created_at = NOW() - INTERVAL '3 days'`)
			require.NoError(t, err)
			deleted, err := NewStore(pool).DeleteIdempotencyKeys(ctx, 0, 10)
			require.NoError(t, err)
			assert.EqualValues(t, 1, deleted)
		})
	}
}

// The names the library creates are the ones the README and the lint rule say.
func TestPG_LibraryObjectNamesCarryNoServiceName(t *testing.T) {
	url, pool := testpg.New(t)
	require.NoError(t, Migrate(url))
	ctx := context.Background()

	rows, err := pool.Query(ctx, `SELECT relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND relkind IN ('r', 'i') ORDER BY relname`)
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		names = append(names, s)
	}
	assert.NotContains(t, names, "idempotency_keys", "that name belongs to the services")
	assert.Contains(t, names, "common_idempotency_keys")
	assert.Contains(t, names, "common_idempotency_keys_pkey")
	assert.Contains(t, names, "idx_common_idempotency_keys_reaper")
}

// The service's own migration creates the same outbox_messages table and
// indexes (chat-service's 006_create_outbox.sql; auth-service's 00001). Either
// order must work, on an empty database and on one that already has the table.
func TestPG_MigrateIsSafeInEitherOrderWithAServicesOwnOutboxMigration(t *testing.T) {
	// The service's copy of 001: the guards are `IF NOT EXISTS` on the indexes.
	const serviceOutbox = `
		CREATE TABLE IF NOT EXISTS outbox_messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), aggregate_type VARCHAR(64) NOT NULL,
			aggregate_id VARCHAR(64) NOT NULL, event_type VARCHAR(128) NOT NULL, topic VARCHAR(128) NOT NULL,
			key VARCHAR(128) NOT NULL DEFAULT '', payload JSONB NOT NULL, headers JSONB NOT NULL DEFAULT '{}',
			status VARCHAR(16) NOT NULL DEFAULT 'pending', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			sent_at TIMESTAMPTZ);
		CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_messages (created_at) WHERE status = 'pending';
		CREATE INDEX IF NOT EXISTS idx_outbox_reaper ON outbox_messages (sent_at) WHERE status = 'sent'`

	t.Run("outbox.Migrate first, service migration after (chat-service today)", func(t *testing.T) {
		url, pool := testpg.New(t)
		require.NoError(t, Migrate(url))
		_, err := pool.Exec(context.Background(), serviceOutbox)
		require.NoError(t, err)
		require.NoError(t, Migrate(url))
	})
	t.Run("service migration first, outbox.Migrate after", func(t *testing.T) {
		url, pool := testpg.New(t)
		_, err := pool.Exec(context.Background(), serviceOutbox)
		require.NoError(t, err)
		require.NoError(t, Migrate(url), "go-common's 001 must not fail on the service's identical table and indexes")
		var n int
		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_outbox_pending_topic'`).Scan(&n))
		assert.Equal(t, 1, n)
	})
}

// Two pods that boot together on a fresh database both run outbox.Migrate.
func TestPG_MigrateRunsOnceWhenPodsBootTogether(t *testing.T) {
	url, pool := testpg.New(t)

	const pods = 6
	errs := make([]error, pods)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < pods; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = Migrate(url)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, fmt.Sprintf("pod %d", i))
	}
	var applied int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM goose_db_version_outbox WHERE version_id > 0`).Scan(&applied))
	assert.Equal(t, 7, applied, "every migration is recorded exactly once")
}

func indexIsValid(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var valid bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)`, name).Scan(&valid))
	return valid
}

// R8: an interrupted CONCURRENTLY build leaves an INVALID index of the same
// name; `IF NOT EXISTS` would keep it for ever, and the relay's claim would run
// without its index.
func TestPG_MigrateRebuildsAnInvalidLeftoverIndex(t *testing.T) {
	url, pool := testpg.New(t)
	ctx := context.Background()
	require.NoError(t, Migrate(url))
	require.True(t, indexIsValid(t, pool, "idx_outbox_pending_topic"))

	// The state after a killed build: the index is invalid and 007 not recorded.
	_, err := pool.Exec(ctx, `UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'idx_outbox_pending_topic'::regclass`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM goose_db_version_outbox WHERE version_id = 7`)
	require.NoError(t, err)
	require.False(t, indexIsValid(t, pool, "idx_outbox_pending_topic"))

	require.NoError(t, Migrate(url))

	assert.True(t, indexIsValid(t, pool, "idx_outbox_pending_topic"), "the invalid leftover is dropped and rebuilt")
}

// R8: migration 007 builds its index without blocking the service's writes. A
// plain CREATE INDEX queues behind an open writer and every later INSERT queues
// behind it (that is how a large outbox_messages stalled every business write).
func TestPG_Migration007DoesNotBlockWritesWhileItBuilds(t *testing.T) {
	url, pool := testpg.New(t)
	ctx := context.Background()
	migrateUpTo(t, url, 6)

	// An open writer: its uncommitted row keeps the index build waiting.
	holder, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()
	_, err = holder.Exec(ctx, `INSERT INTO outbox_messages (aggregate_type, aggregate_id, event_type, topic, payload)
		VALUES ('a', 'a', 'e', 't', '{}')`)
	require.NoError(t, err)

	migrated := make(chan error, 1)
	go func() { migrated <- Migrate(url) }()
	require.Never(t, func() bool { return len(migrated) > 0 }, 500*time.Millisecond, 50*time.Millisecond,
		"the build waits for the open writer")

	// A business write while the build waits must go through at once.
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = pool.Exec(writeCtx, `INSERT INTO outbox_messages (aggregate_type, aggregate_id, event_type, topic, payload)
		VALUES ('b', 'b', 'e', 't', '{}')`)
	require.NoError(t, err, "an INSERT must not queue behind the index build")

	require.NoError(t, holder.Commit(ctx))
	select {
	case err := <-migrated:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("Migrate did not finish after the writer committed")
	}
	assert.True(t, indexIsValid(t, pool, "idx_outbox_pending_topic"))
}
