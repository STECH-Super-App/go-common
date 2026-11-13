// Package testpg gives go-common's own integration tests a throwaway Postgres
// database. It is internal to go-common: a service has its own harness.
//
// The server comes from GOCOMMON_TEST_POSTGRES_URL (any database on it; the
// helper only uses it to CREATE and DROP databases of its own). Unset, the test
// is skipped — and FAILED instead when REQUIRE_DB=1, the CI setting, so a
// workflow that lost its Postgres service container cannot go quietly green.
//
// Never point it at a shared stack (Critical Rule 10); CI uses a service
// container, a developer a private one.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvURL names the variable holding the admin connection URL.
const EnvURL = "GOCOMMON_TEST_POSTGRES_URL"

// New creates an empty database named gc_<random>, registers its removal for
// test cleanup, and returns its connection URL and a pool on it.
func New(t *testing.T) (dbURL string, pool *pgxpool.Pool) {
	t.Helper()

	adminURL := os.Getenv(EnvURL)
	if adminURL == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatalf("%s is not set but REQUIRE_DB=1: the Postgres integration tests must run", EnvURL)
		}
		t.Skipf("%s not set: skipping the Postgres integration test", EnvURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to %s: %v", EnvURL, err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	name := "gc_" + randomHex(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			t.Logf("cleanup: connect: %v", err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		if _, err := c.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}
	u.Path = "/" + name
	dbURL = u.String()

	pool, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool on %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return dbURL, pool
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
