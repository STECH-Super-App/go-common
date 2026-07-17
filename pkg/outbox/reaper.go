package outbox

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// DefaultClaimTimeout is how long a message may sit in 'processing' before the
// Reaper assumes the claiming relay crashed and releases it back to 'pending'.
// It must comfortably exceed the longest plausible ClaimPending → MarkSentBatch
// window (a poll cycle is sub-second) so a live relay's claims are never stolen.
const DefaultClaimTimeout = 5 * time.Minute

// ReaperConfig tunes the cleanup behavior.
type ReaperConfig struct {
	Interval  time.Duration // How often to run cleanup (default: 5m)
	Retention time.Duration // How long to keep sent messages (default: 72h)
	// ClaimTimeout is the age after which a 'processing' claim is considered
	// orphaned (relay crashed mid-batch) and released back to 'pending'.
	// Zero or negative falls back to DefaultClaimTimeout — services that build
	// ReaperConfig literally without this field keep the crash backstop.
	ClaimTimeout time.Duration
}

// reaperStore is the narrow Store surface the reaper depends on. Unexported so
// the public API stays NewReaper(*Store, ...); tests substitute an in-package
// fake without a database.
type reaperStore interface {
	DeleteSent(ctx context.Context, retention time.Duration) (int64, error)
	ReleaseStuck(ctx context.Context, olderThan time.Duration) (int64, error)
}

// processedCleaner is the dedup-table cleanup surface, satisfied by
// *Deduplicator. Unexported for the same fake-in-tests reason as reaperStore.
type processedCleaner interface {
	CleanupProcessed(ctx context.Context, retention time.Duration) (int64, error)
}

// ReaperOption customizes optional Reaper behavior.
type ReaperOption func(*Reaper)

// WithDeduplicator attaches a Deduplicator whose processed_outbox_messages
// table the reaper cleans up on every cycle, using the same Retention as the
// outbox table. Without it the dedup table grows forever — outbox.New wires
// this automatically; only hand-built reapers need to pass it explicitly.
func WithDeduplicator(d *Deduplicator) ReaperOption {
	return func(r *Reaper) {
		if d != nil {
			r.dedup = d
		}
	}
}

// Reaper periodically deletes old sent messages from the outbox table (and,
// when configured, old rows from the dedup table) to prevent table bloat, and
// releases 'processing' claims orphaned by crashed relays. It is fully
// self-contained — no external cron needed.
type Reaper struct {
	store  reaperStore
	dedup  processedCleaner // nil when no Deduplicator was attached
	logger *zap.Logger
	cfg    ReaperConfig
}

// NewReaper creates a new outbox reaper.
func NewReaper(store *Store, logger *zap.Logger, cfg ReaperConfig, opts ...ReaperOption) *Reaper {
	if cfg.ClaimTimeout <= 0 {
		cfg.ClaimTimeout = DefaultClaimTimeout
	}

	r := &Reaper{
		store:  store,
		logger: logger,
		cfg:    cfg,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Run starts the reaper loop. Blocks until ctx is cancelled.
func (r *Reaper) Run(ctx context.Context) error {
	r.logger.Info("outbox reaper started",
		zap.Duration("interval", r.cfg.Interval),
		zap.Duration("retention", r.cfg.Retention),
		zap.Duration("claim_timeout", r.cfg.ClaimTimeout),
	)

	// Run immediately on startup, then on interval.
	r.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox reaper stopped")
			return ctx.Err()
		case <-time.After(r.cfg.Interval):
			r.cleanup(ctx)
		}
	}
}

// cleanup executes a single reaper cycle: reap old sent messages, release
// stuck claims, and (when a Deduplicator is attached) trim the dedup table.
// Each step runs even if an earlier one failed — they are independent.
func (r *Reaper) cleanup(ctx context.Context) {
	deleted, err := r.store.DeleteSent(ctx, r.cfg.Retention)
	if err != nil {
		r.logger.Error("outbox reaper: cleanup failed", zap.Error(err))
	} else if deleted > 0 {
		r.logger.Info("outbox reaper: cleaned up messages",
			zap.Int64("deleted", deleted),
			zap.Duration("retention", r.cfg.Retention),
		)
	}

	released, err := r.store.ReleaseStuck(ctx, r.cfg.ClaimTimeout)
	if err != nil {
		r.logger.Error("outbox reaper: release stuck claims failed", zap.Error(err))
	} else if released > 0 {
		// A non-zero count means a relay died mid-batch (or a claim outlived
		// the timeout) — worth surfacing at Warn, not Info.
		r.logger.Warn("outbox reaper: released stuck claims back to pending",
			zap.Int64("released", released),
			zap.Duration("claim_timeout", r.cfg.ClaimTimeout),
		)
	}

	if r.dedup == nil {
		return
	}
	cleaned, err := r.dedup.CleanupProcessed(ctx, r.cfg.Retention)
	if err != nil {
		r.logger.Error("outbox reaper: dedup table cleanup failed", zap.Error(err))
	} else if cleaned > 0 {
		r.logger.Info("outbox reaper: cleaned up processed event ids",
			zap.Int64("deleted", cleaned),
			zap.Duration("retention", r.cfg.Retention),
		)
	}
}
