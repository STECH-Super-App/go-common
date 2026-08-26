package outbox

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// Outbox is the top-level coordinator for the transactional outbox pattern.
// It wires together the Publisher (for transactional writes), the Relay
// (for Kafka forwarding), and the Reaper (for table cleanup).
//
// Usage in a service's main.go:
//
//	ob := outbox.New(pool, kafkaWriter, zlog, outbox.DefaultConfig(), cfg.Kafka.Topic)
//	stopOutbox := ob.Start(ctx)
//	defer stopOutbox()
//
// Start also runs the metrics sampler, so a service gets the §3.3 outbox
// families by wiring nothing extra — there is no separate StartMetrics a repo
// could forget to call.
type Outbox struct {
	// Publisher is the public API for transactional event publishing.
	// Services use this within RunInTx to atomically write events.
	Publisher *Publisher

	store   *Store
	relay   *Relay
	reaper  *Reaper
	sampler *sampler
	logger  *zap.Logger
}

// New creates the full outbox subsystem.
// The kafkaWriter should have no fixed Topic — each message carries its own topic.
// defaultTopic is the Kafka destination applied when PublishProtoOptions.Topic is empty.
func New(pool *pgxpool.Pool, kafkaWriter *kafka.Writer, logger *zap.Logger, cfg *Config, defaultTopic string) *Outbox {
	store := NewStore(pool)

	return &Outbox{
		Publisher: NewPublisher(store, defaultTopic),
		store:     store,
		relay:     NewRelay(store, kafkaWriter, logger, cfg.Relay),
		reaper:    NewReaper(store, logger, cfg.Reaper),
		sampler:   newSampler(store.PendingStats, cfg.MetricsInterval, logger),
		logger:    logger,
	}
}

// Start launches the relay, the reaper and the metrics sampler as background
// goroutines. Returns a stop function that cancels all three and waits for
// them to exit.
//
// Call pattern:
//
//	stop := ob.Start(ctx)
//	defer stop() // blocks until relay, reaper and sampler are fully stopped
func (o *Outbox) Start(ctx context.Context) (stop func()) {
	relayCtx, relayCancel := context.WithCancel(ctx)
	reaperCtx, reaperCancel := context.WithCancel(ctx)
	samplerCtx, samplerCancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		if err := o.relay.Run(relayCtx); err != nil && relayCtx.Err() == nil {
			o.logger.Error("outbox relay exited with error", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		if err := o.reaper.Run(reaperCtx); err != nil && reaperCtx.Err() == nil {
			o.logger.Error("outbox reaper exited with error", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		o.sampler.run(samplerCtx)
	}()

	o.logger.Info("outbox subsystem started (relay + reaper + metrics sampler)")

	return func() {
		relayCancel()
		reaperCancel()
		samplerCancel()
		wg.Wait()
		o.logger.Info("outbox subsystem stopped")
	}
}
