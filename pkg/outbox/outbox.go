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
// TODO: Add Prometheus metrics (pending gauge, relay counter, reaper counter)
// when metric collection infrastructure matures.
type Outbox struct {
	// Publisher is the public API for transactional event publishing.
	// Services use this within RunInTx to atomically write events.
	Publisher *Publisher

	store  *Store
	relay  *Relay
	reaper *Reaper
	logger *zap.Logger
}

// New creates the full outbox subsystem.
// The kafkaWriter should have no fixed Topic — each message carries its own topic.
// defaultTopic is the Kafka destination applied when PublishOptions.Topic is empty.
func New(pool *pgxpool.Pool, kafkaWriter *kafka.Writer, logger *zap.Logger, cfg *Config, defaultTopic string) *Outbox {
	store := NewStore(pool)

	return &Outbox{
		Publisher: NewPublisher(store, defaultTopic),
		store:     store,
		relay:     NewRelay(store, kafkaWriter, logger, cfg.Relay),
		reaper:    NewReaper(store, logger, cfg.Reaper),
		logger:    logger,
	}
}

// Start launches the relay and reaper as background goroutines.
// Returns a stop function that cancels both goroutines and waits for them to exit.
//
// Call pattern:
//
//	stop := ob.Start(ctx)
//	defer stop() // blocks until relay and reaper are fully stopped
func (o *Outbox) Start(ctx context.Context) (stop func()) {
	relayCtx, relayCancel := context.WithCancel(ctx)
	reaperCtx, reaperCancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(2)

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

	o.logger.Info("outbox subsystem started (relay + reaper)")

	return func() {
		relayCancel()
		reaperCancel()
		wg.Wait()
		o.logger.Info("outbox subsystem stopped")
	}
}
