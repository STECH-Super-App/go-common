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

// Reaper periodically deletes old sent messages from the outbox table
// to prevent table bloat. It is fully self-contained — no external cron needed.
type Reaper struct {
	store  reaperStore
	logger *zap.Logger
	cfg    ReaperConfig

	// processedOff is set once the claim table turned out not to exist, so the
	// reaper stops asking instead of logging an error every cycle.
	processedOff bool
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

func (r *Reaper) processedBatchSize() int {
	if r.cfg.ProcessedBatchSize > 0 {
		return r.cfg.ProcessedBatchSize
	}
	return DefaultProcessedBatchSize
}

// cleanup executes a single reaper cycle: old sent outbox rows, then old
// deduplicator claims.
func (r *Reaper) cleanup(ctx context.Context) {
	r.cleanupSent(ctx)
	r.cleanupProcessed(ctx)
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
