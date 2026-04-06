package outbox

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// outboxGooseTable is the dedicated goose version table for outbox migrations.
// Using a separate table prevents collision with service-specific migrations
// that share the same database.
const outboxGooseTable = "goose_db_version_outbox"

// Migrate runs the outbox table migration against the given Postgres database.
// Safe to call repeatedly — goose tracks which migrations have already been applied.
//
// Call this in main.go after the service's own migrations:
//
//	if err := outbox.Migrate(cfg.Postgres.URL); err != nil {
//	    zlog.Fatal("outbox migration failed", zap.Error(err))
//	}
func Migrate(postgresURL string) error {
	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		return fmt.Errorf("outbox: open db for migration: %w", err)
	}
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations,
		goose.WithTableName(outboxGooseTable),
	)
	if err != nil {
		return fmt.Errorf("outbox: create migration provider: %w", err)
	}

	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("outbox: run migrations: %w", err)
	}

	return nil
}
