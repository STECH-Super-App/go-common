package outbox

import (
	"time"

	"github.com/STECH-Super-App/go-common/pkg/config"
)

// Config holds all outbox subsystem configuration.
type Config struct {
	Relay  RelayConfig
	Reaper ReaperConfig
	// MetricsInterval is how often the pending-backlog gauges are sampled.
	// Zero means DefaultMetricsInterval — a hand-built Config must not be able
	// to panic the ticker.
	MetricsInterval time.Duration
}

// DefaultConfig returns production-ready defaults, overridable via environment variables.
//
// Environment variables:
//
//	OUTBOX_POLL_INTERVAL  - Relay polling frequency     (default: "1s")
//	OUTBOX_BATCH_SIZE     - Messages per poll cycle     (default: 100)
//	OUTBOX_SHUTDOWN_FLUSH_TIMEOUT - In-flight batch flush after cancel (default: "5s")
//	OUTBOX_REAPER_INTERVAL - Cleanup schedule           (default: "5m")
//	OUTBOX_RETENTION      - Sent message retention      (default: "72h")
//	OUTBOX_PROCESSED_RETENTION - Deduplicator claim retention (default: "720h")
//	OUTBOX_PROCESSED_BATCH_SIZE - Claims deleted per statement (default: 5000)
//	OUTBOX_METRICS_INTERVAL - Backlog gauge sampling    (default: "15s")
func DefaultConfig() *Config {
	return &Config{
		Relay: RelayConfig{
			PollInterval: config.GetEnvDuration("OUTBOX_POLL_INTERVAL", DefaultPollInterval),
			BatchSize:    config.GetEnvInt("OUTBOX_BATCH_SIZE", DefaultBatchSize),
			ShutdownFlushTimeout: config.GetEnvDuration(
				"OUTBOX_SHUTDOWN_FLUSH_TIMEOUT", DefaultShutdownFlushTimeout),
		},
		Reaper: ReaperConfig{
			Interval:           config.GetEnvDuration("OUTBOX_REAPER_INTERVAL", DefaultReaperInterval),
			Retention:          config.GetEnvDuration("OUTBOX_RETENTION", DefaultRetention),
			ProcessedRetention: config.GetEnvDuration("OUTBOX_PROCESSED_RETENTION", DefaultProcessedRetention),
			ProcessedBatchSize: config.GetEnvInt("OUTBOX_PROCESSED_BATCH_SIZE", DefaultProcessedBatchSize),
		},
		MetricsInterval: config.GetEnvDuration("OUTBOX_METRICS_INTERVAL", DefaultMetricsInterval),
	}
}
