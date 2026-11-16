// Package db provides database connection factories.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Reasons of the typed errors the factories return (Critical Rule 7). A bad
// URL is a deployment mistake and a failed connect an unavailable dependency;
// neither is a client error, so both are 500s with the driver error as Cause.
const (
	ReasonConfigInvalid = "COMMON_DB_CONFIG_INVALID"
	ReasonUnavailable   = "COMMON_DB_UNAVAILABLE"
)

func dbError(reason, message string, cause error) *apperr.AppError {
	return apperr.New(500).Reason(reason).Message(message).Cause(cause).Build()
}

// PostgresConfig holds configuration for the Postgres connection. A zero field
// means "not set": the value parsed from URL (pool_max_conns=…) or pgx's own
// default stays. Only a non-zero field overrides it.
type PostgresConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// poolConfig parses URL and applies the non-zero fields on top of what pgx
// parsed. Applying a zero unconditionally used to make an unset MaxConns fail
// pool creation and an unset MaxConnLifetime expire every connection on release
// (SL-22).
func poolConfig(config PostgresConfig) (*pgxpool.Config, error) {
	pgxConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, dbError(ReasonConfigInvalid, "failed to parse postgres config", err)
	}

	if config.MaxConns > 0 {
		pgxConfig.MaxConns = config.MaxConns
	}
	if config.MinConns > 0 {
		pgxConfig.MinConns = config.MinConns
	}
	if config.MaxConnLifetime > 0 {
		pgxConfig.MaxConnLifetime = config.MaxConnLifetime
	}
	if config.MaxConnIdleTime > 0 {
		pgxConfig.MaxConnIdleTime = config.MaxConnIdleTime
	}
	return pgxConfig, nil
}

// NewPostgres creates a new Postgres connection pool and pings it. A pool that
// fails its ping is closed before the error is returned.
func NewPostgres(ctx context.Context, config PostgresConfig) (*pgxpool.Pool, error) {
	pgxConfig, err := poolConfig(config)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxConfig)
	if err != nil {
		return nil, dbError(ReasonConfigInvalid, "failed to create postgres pool", err)
	}

	if err := pingOrClose(func() error { return pool.Ping(ctx) }, pool.Close); err != nil {
		return nil, dbError(ReasonUnavailable, "failed to ping postgres", err)
	}

	return pool, nil
}

// pingOrClose runs ping and, when it fails, closes the resource it belongs to,
// so a failed start-up does not leak the pool's or client's background
// goroutines and connections.
func pingOrClose(ping func() error, closeFn func()) error {
	if err := ping(); err != nil {
		closeFn()
		return err
	}
	return nil
}
