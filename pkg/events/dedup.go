package events

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Tx is the transaction a transaction-form handler (HandleTx) runs in: the one
// the deduplicator opened and recorded the event id in. Everything the handler
// writes — rows, outbox events published with outbox.Publisher.PublishProto —
// goes through it, and commits or rolls back together with the "processed"
// mark (SL-04).
//
// It is an alias of pgx.Tx, so it satisfies outbox.Tx and every repository
// method that takes one.
type Tx = pgx.Tx

// Deduplicator guarantees that a handler's effect happens once per
// (consumer, event) even though Kafka delivers at least once. NewDispatcher
// takes one as a REQUIRED argument, and the interface is sealed (its method is
// unexported): the only values are the ones this package builds, so a consumer
// cannot be wired without the protection and cannot bring its own.
//
//   - NewPostgresDeduplicator — production. Needs the go-common migrations
//     (outbox.Migrate creates processed_events). A service with no database of
//     its own gets a small one for this; there is no dispatcher without it.
//   - NewMemoryDeduplicator — tests only; it dies with the process and gives
//     handlers no transaction. Lint bans it outside _test.go files.
type Deduplicator interface {
	// process runs body at most once per (consumer, eventID). ran reports
	// whether body ran, successfully or not; ran false with a nil error means
	// the event was already processed. When body fails the claim is rolled back
	// with it, so a retry runs the handler again.
	process(ctx context.Context, consumer, eventID string, body func(ctx context.Context, tx Tx) error) (ran bool, err error)

	// processed reports, read-only, whether the event is already recorded as processed
	// for this consumer. It holds no transaction and no connection once it
	// returns. It is only an early exit for HandlePrepared (a redelivery skips the
	// slow phase); the claim inside process stays the one that decides.
	processed(ctx context.Context, consumer, eventID string) (bool, error)
}

// legacyClaimsTable is the table the previous deduplicator wrote to. While it
// exists the new deduplicator treats an event recorded there as processed, so a
// message redelivered across the upgrade is not run a second time.
const legacyClaimsTable = "processed_outbox_messages"

// PostgresDeduplicator is the production Deduplicator: one row per
// (consumer, event_id) in processed_events, inserted in the transaction the
// handler runs in.
type PostgresDeduplicator struct {
	pool *pgxpool.Pool
	log  *zap.Logger

	legacyMu     sync.Mutex
	legacyKnown  bool
	legacyExists bool
}

// NewPostgresDeduplicator builds the deduplicator over the service's own
// database pool. It panics on a nil pool: a consumer without a database cannot
// deduplicate, and the dispatcher must not start pretending it can.
func NewPostgresDeduplicator(pool *pgxpool.Pool) *PostgresDeduplicator {
	if pool == nil {
		panic(internalError(ReasonDedupFailed, "events.NewPostgresDeduplicator: pool is nil: a consumer needs a database for its processed_events table", nil))
	}
	return &PostgresDeduplicator{pool: pool, log: zap.NewNop()}
}

// Ready checks that the processed_events table exists, so a service whose
// outbox.Migrate did not run fails at start-up instead of dead-lettering every
// message after the retry window. Call it once, after outbox.Migrate, and treat
// an error as fatal.
func (d *PostgresDeduplicator) Ready(ctx context.Context) error {
	var exists bool
	if err := d.pool.QueryRow(ctx, `SELECT to_regclass('processed_events') IS NOT NULL`).Scan(&exists); err != nil {
		return internalError(ReasonDedupFailed, "events dedup: readiness check", err)
	}
	if !exists {
		return internalError(ReasonDedupFailed,
			"events dedup: table processed_events does not exist: call outbox.Migrate before starting consumers", nil)
	}
	return nil
}

func (d *PostgresDeduplicator) process(ctx context.Context, consumer, eventID string, body func(context.Context, Tx) error) (bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return false, internalError(ReasonDedupFailed, "events dedup: begin transaction", err)
	}
	// Rollback after a successful Commit is a no-op. It must work after a
	// timeout or a shutdown cancelled ctx, so it gets its own bounded context.
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if eventID == "" {
		// A producer that sent no event_id cannot be deduplicated; process it
		// anyway (a dropped event is worse than a repeated one), in a
		// transaction so the handler still gets one.
		d.log.Warn("events dedup: message has no event_id; processing it without a claim")
	} else {
		claimed, err := d.claim(ctx, tx, consumer, eventID)
		if err != nil {
			return false, err
		}
		if !claimed {
			return false, nil
		}
	}

	if err := body(ctx, tx); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, internalError(ReasonDedupFailed, "events dedup: commit transaction", err)
	}
	return true, nil
}

// processed is the read-only "already processed?" check of HandlePrepared: the
// processed_events row, or — across the upgrade — the previous deduplicator's.
// Each query borrows a pooled connection for its own duration only.
func (d *PostgresDeduplicator) processed(ctx context.Context, consumer, eventID string) (bool, error) {
	var done bool
	const probe = `SELECT EXISTS (SELECT 1 FROM processed_events WHERE consumer = $1 AND event_id = $2)`
	if err := d.pool.QueryRow(ctx, probe, consumer, eventID).Scan(&done); err != nil {
		return false, internalError(ReasonDedupFailed, "events dedup: read processed events", err)
	}
	if done {
		return true, nil
	}
	if _, err := uuid.Parse(eventID); err == nil && d.hasLegacyTable(ctx) {
		const legacy = `SELECT EXISTS (SELECT 1 FROM ` + legacyClaimsTable + ` WHERE event_id = $1::uuid)`
		if err := d.pool.QueryRow(ctx, legacy, eventID).Scan(&done); err != nil {
			return false, internalError(ReasonDedupFailed, "events dedup: read legacy claims", err)
		}
	}
	return done, nil
}

// claim takes the event for this consumer: the INSERT is the compare-and-set
// (the primary key lets exactly one deliverer take the row; a concurrent second
// delivery waits on the index until the first transaction ends). It reports
// false when the event was already processed.
func (d *PostgresDeduplicator) claim(ctx context.Context, tx pgx.Tx, consumer, eventID string) (bool, error) {
	const insert = `
		INSERT INTO processed_events (consumer, event_id, processed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT DO NOTHING`
	tag, err := tx.Exec(ctx, insert, consumer, eventID)
	if err != nil {
		return false, internalError(ReasonDedupFailed, "events dedup: claim event", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	// Recorded by the previous deduplicator? Only a UUID can be: that table's
	// key is a uuid column.
	if _, err := uuid.Parse(eventID); err == nil && d.hasLegacyTable(ctx) {
		var seen bool
		const probe = `SELECT EXISTS (SELECT 1 FROM ` + legacyClaimsTable + ` WHERE event_id = $1::uuid)`
		if err := tx.QueryRow(ctx, probe, eventID).Scan(&seen); err != nil {
			return false, internalError(ReasonDedupFailed, "events dedup: read legacy claims", err)
		}
		if seen {
			// Leave the new row in place: the transaction rolls back below
			// only on error, and this claim should stand so later redeliveries
			// stop at the first INSERT.
			return false, d.commitClaimOnly(ctx, tx)
		}
	}
	return true, nil
}

// commitClaimOnly is the legacy-hit path: the event was processed under the
// previous deduplicator, so the handler must not run, but the new claim row is
// kept (committed) to make the next redelivery a cheap first-INSERT conflict.
func (d *PostgresDeduplicator) commitClaimOnly(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return internalError(ReasonDedupFailed, "events dedup: commit legacy-hit claim", err)
	}
	return nil
}

// hasLegacyTable reports whether the previous deduplicator's table exists in
// this database. The answer is cached once it is known; a failed lookup is not
// cached (and counts as "no": the new claim row alone still deduplicates every
// delivery from now on).
func (d *PostgresDeduplicator) hasLegacyTable(ctx context.Context) bool {
	d.legacyMu.Lock()
	defer d.legacyMu.Unlock()
	if d.legacyKnown {
		return d.legacyExists
	}
	var exists bool
	if err := d.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, legacyClaimsTable).Scan(&exists); err != nil {
		return false
	}
	d.legacyKnown, d.legacyExists = true, exists
	return exists
}

// MemoryDeduplicator is a Deduplicator for unit tests: it remembers processed
// event ids in memory and gives handlers a nil Tx. It is NOT for production —
// it forgets on restart and is not atomic with anything; the lint config bans
// it outside _test.go files.
type MemoryDeduplicator struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewMemoryDeduplicator returns an empty in-memory deduplicator. Tests only.
func NewMemoryDeduplicator() *MemoryDeduplicator {
	return &MemoryDeduplicator{seen: make(map[string]struct{})}
}

func (m *MemoryDeduplicator) processed(_ context.Context, consumer, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, dup := m.seen[consumer+"/"+eventID]
	return dup, nil
}

func (m *MemoryDeduplicator) process(ctx context.Context, consumer, eventID string, body func(context.Context, Tx) error) (bool, error) {
	key := consumer + "/" + eventID
	if eventID != "" {
		m.mu.Lock()
		_, dup := m.seen[key]
		m.mu.Unlock()
		if dup {
			return false, nil
		}
	}
	if err := body(ctx, nil); err != nil {
		return true, err
	}
	if eventID != "" {
		m.mu.Lock()
		m.seen[key] = struct{}{}
		m.mu.Unlock()
	}
	return true, nil
}
