package outbox

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// ReaperConfig tunes the cleanup behavior.
type ReaperConfig struct {
	Interval  time.Duration // How often to run cleanup (default: 5m)
	Retention time.Duration // How long to keep sent messages (default: 72h)
}

// Reaper periodically deletes old sent messages from the outbox table
// to prevent table bloat. It is fully self-contained — no external cron needed.
type Reaper struct {
	store  *Store
	logger *zap.Logger
	cfg    ReaperConfig
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
		zap.Duration("interval", r.cfg.Interval),
		zap.Duration("retention", r.cfg.Retention),
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

// cleanup executes a single reaper cycle.
func (r *Reaper) cleanup(ctx context.Context) {
	deleted, err := r.store.DeleteSent(ctx, r.cfg.Retention)
	if err != nil {
		r.logger.Error("outbox reaper: cleanup failed", zap.Error(err))
		return
	}

	recordReaped(deleted)

	if deleted > 0 {
		r.logger.Info("outbox reaper: cleaned up messages",
			zap.Int64("deleted", deleted),
			zap.Duration("retention", r.cfg.Retention),
		)
	}
}
