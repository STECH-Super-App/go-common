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
//	OUTBOX_REAPER_INTERVAL - Cleanup schedule           (default: "5m")
//	OUTBOX_RETENTION      - Sent message retention      (default: "72h")
//	OUTBOX_METRICS_INTERVAL - Backlog gauge sampling    (default: "15s")
func DefaultConfig() *Config {
	return &Config{
		Relay: RelayConfig{
			PollInterval: config.GetEnvDuration("OUTBOX_POLL_INTERVAL", 1*time.Second),
			BatchSize:    config.GetEnvInt("OUTBOX_BATCH_SIZE", 100),
		},
		Reaper: ReaperConfig{
			Interval:  config.GetEnvDuration("OUTBOX_REAPER_INTERVAL", 5*time.Minute),
			Retention: config.GetEnvDuration("OUTBOX_RETENTION", 72*time.Hour),
		},
		MetricsInterval: config.GetEnvDuration("OUTBOX_METRICS_INTERVAL", DefaultMetricsInterval),
	}
}
