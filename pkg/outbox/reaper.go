package outbox

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

// Reaper defaults. A zero or negative value in ReaperConfig means the default:
// a hand-built config must not turn Retention into "delete every sent row now"
// or Interval into a hot loop (SL-15).
const (
	DefaultReaperInterval     = 5 * time.Minute
	DefaultRetention          = 72 * time.Hour
	DefaultProcessedRetention = 30 * 24 * time.Hour
	DefaultProcessedBatchSize = 5000

	// DefaultIdempotencyRetention is how long an idempotency key lives: a client
	// retry inside it replays the first result, after it the key is free again.
	DefaultIdempotencyRetention = 24 * time.Hour

	// maxProcessedBatchesPerCycle caps one cycle's claim cleanup at
	// maxProcessedBatchesPerCycle × batch rows, so the first run against a table
	// that has grown for months works through it over several cycles instead
	// of holding one long delete loop.
	maxProcessedBatchesPerCycle = 100
)

// ReaperConfig tunes the cleanup behavior.
type ReaperConfig struct {
	Interval  time.Duration // How often to run cleanup (default: 5m)
	Retention time.Duration // How long to keep sent messages (default: 72h)

	// ProcessedRetention is how long processed_outbox_messages claims are kept
	// (default: 30d). It must outlive the longest period a message can still be
	// redelivered — the Kafka topic retention plus the DLQ replay window —
	// because a claim deleted early lets a redelivery run its handler again.
	ProcessedRetention time.Duration
	// ProcessedBatchSize bounds one DELETE of old claims (default: 5000).
	ProcessedBatchSize int

	// IdempotencyRetention is how long idempotency_keys rows are kept (default:
	// 24h, DefaultIdempotencyRetention).
	IdempotencyRetention time.Duration
}

// reaperStore is the slice of *Store the reaper uses — a seam so the outbox
// subsystem can be run in unit tests without Postgres. *Store satisfies it.
type reaperStore interface {
	DeleteSent(ctx context.Context, retention time.Duration) (int64, error)
}

// processedStore is the optional second capability of the reaper's store:
// deleting old deduplicator claims. *Store satisfies it.
type processedStore interface {
	DeleteProcessed(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

// eventsStore and idempotencyStore are the same kind of optional capability for
// the two tables release 2 added. *Store satisfies all three.
type eventsStore interface {
	DeleteProcessedEvents(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

type idempotencyStore interface {
	DeleteIdempotencyKeys(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

// Reaper periodically deletes old sent messages from the outbox table
// to prevent table bloat. It is fully self-contained — no external cron needed.
type Reaper struct {
	store  reaperStore
	logger *zap.Logger
	cfg    ReaperConfig

	// The *Off flags are set once the table turned out not to exist, so the
	// reaper stops asking instead of logging an error every cycle. A service
	// that runs no consumers has no deduplicator table to reap.
	processedOff   bool
	eventsOff      bool
	idempotencyOff bool
}

// NewReaper creates a new outbox reaper.
func NewReaper(store *Store, logger *zap.Logger, cfg ReaperConfig) *Reaper {
	return &Reaper{
		store:  store,
		logger: logger,
		cfg:    cfg,
	}
}

// Run starts the reaper loop. Blocks until ctx is cancelled.
func (r *Reaper) Run(ctx context.Context) error {
	r.logger.Info("outbox reaper started",
		zap.Duration("interval", r.interval()),
		zap.Duration("retention", r.retention()),
		zap.Duration("processed_retention", r.processedRetention()),
	)

	// Run immediately on startup, then on interval.
	r.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox reaper stopped")
			return ctx.Err()
		case <-time.After(r.interval()):
			r.cleanup(ctx)
		}
	}
}

// interval, retention, processedRetention and processedBatchSize apply the
// defaults for non-positive config values.
func (r *Reaper) interval() time.Duration {
	if r.cfg.Interval > 0 {
		return r.cfg.Interval
	}
	return DefaultReaperInterval
}

func (r *Reaper) retention() time.Duration {
	if r.cfg.Retention > 0 {
		return r.cfg.Retention
	}
	return DefaultRetention
}

func (r *Reaper) processedRetention() time.Duration {
	if r.cfg.ProcessedRetention > 0 {
		return r.cfg.ProcessedRetention
	}
	return DefaultProcessedRetention
}

func (r *Reaper) idempotencyRetention() time.Duration {
	if r.cfg.IdempotencyRetention > 0 {
		return r.cfg.IdempotencyRetention
	}
	return DefaultIdempotencyRetention
}

func (r *Reaper) processedBatchSize() int {
	if r.cfg.ProcessedBatchSize > 0 {
		return r.cfg.ProcessedBatchSize
	}
	return DefaultProcessedBatchSize
}

// cleanup executes a single reaper cycle: old sent outbox rows, old
// deduplicator claims (both tables) and old idempotency keys.
func (r *Reaper) cleanup(ctx context.Context) {
	r.cleanupSent(ctx)
	r.cleanupProcessed(ctx)
	r.cleanupEvents(ctx)
	r.cleanupIdempotency(ctx)
}

// cleanupEvents deletes processed_events claims (the events deduplicator's
// table) older than ProcessedRetention, and cleanupIdempotency idempotency keys
// older than IdempotencyRetention, each in bounded batches like cleanupProcessed.
func (r *Reaper) cleanupEvents(ctx context.Context) {
	ps, ok := r.store.(eventsStore)
	if !ok || r.eventsOff {
		return
	}
	total, off := r.drain(ctx, "processed events", r.processedRetention(), ps.DeleteProcessedEvents, ErrProcessedTableMissing)
	r.eventsOff = off
	if total > 0 {
		r.logger.Info("outbox reaper: cleaned up processed events",
			zap.Int64("deleted", total), zap.Duration("retention", r.processedRetention()))
	}
}

func (r *Reaper) cleanupIdempotency(ctx context.Context) {
	ps, ok := r.store.(idempotencyStore)
	if !ok || r.idempotencyOff {
		return
	}
	total, off := r.drain(ctx, "idempotency keys", r.idempotencyRetention(), ps.DeleteIdempotencyKeys, ErrIdempotencyTableMissing)
	r.idempotencyOff = off
	if total > 0 {
		r.logger.Info("outbox reaper: cleaned up idempotency keys",
			zap.Int64("deleted", total), zap.Duration("retention", r.idempotencyRetention()))
	}
}

// drain calls del in bounded batches until a short batch, an error, a cancelled
// ctx or the per-cycle cap. It reports the rows deleted and whether the table
// is missing (the caller then stops asking).
func (r *Reaper) drain(
	ctx context.Context,
	what string,
	retention time.Duration,
	del func(context.Context, time.Duration, int) (int64, error),
	missing error,
) (total int64, tableMissing bool) {
	batch := r.processedBatchSize()
	for i := 0; i < maxProcessedBatchesPerCycle && ctx.Err() == nil; i++ {
		n, err := del(ctx, retention, batch)
		if err != nil {
			if errors.Is(err, missing) {
				r.logger.Info("outbox reaper: table for " + what + " does not exist; cleanup disabled")
				return total, true
			}
			r.logger.Error("outbox reaper: "+what+" cleanup failed", zap.Error(err))
			return total, false
		}
		total += n
		if n < int64(batch) {
			break
		}
	}
	return total, false
}

func (r *Reaper) cleanupSent(ctx context.Context) {
	deleted, err := r.store.DeleteSent(ctx, r.retention())
	if err != nil {
		r.logger.Error("outbox reaper: cleanup failed", zap.Error(err))
		return
	}

	recordReaped(deleted)

	if deleted > 0 {
		r.logger.Info("outbox reaper: cleaned up messages",
			zap.Int64("deleted", deleted),
			zap.Duration("retention", r.retention()),
		)
	}
}

// cleanupProcessed deletes processed_outbox_messages claims older than the
// retention, a bounded batch at a time (SL-09). Without it the table grows by
// one row per consumed event forever, and every claim INSERT slows down as the
// primary-key index outgrows memory. A full batch means more rows may remain,
// so the loop runs again; a short batch, an error, a cancelled ctx or the
// per-cycle cap ends the cycle.
func (r *Reaper) cleanupProcessed(ctx context.Context) {
	ps, ok := r.store.(processedStore)
	if !ok || r.processedOff {
		return
	}
	batch := r.processedBatchSize()
	var total int64
	for i := 0; i < maxProcessedBatchesPerCycle && ctx.Err() == nil; i++ {
		n, err := ps.DeleteProcessed(ctx, r.processedRetention(), batch)
		if err != nil {
			if errors.Is(err, ErrProcessedTableMissing) {
				r.processedOff = true
				r.logger.Info("outbox reaper: no processed_outbox_messages table; claim cleanup disabled")
				return
			}
			r.logger.Error("outbox reaper: processed-claim cleanup failed", zap.Error(err))
			break
		}
		total += n
		if n < int64(batch) {
			break
		}
	}
	if total > 0 {
		r.logger.Info("outbox reaper: cleaned up processed claims",
			zap.Int64("deleted", total),
			zap.Duration("retention", r.processedRetention()),
		)
	}
}
