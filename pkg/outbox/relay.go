package outbox

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// RelayConfig tunes the polling relay behavior.
type RelayConfig struct {
	PollInterval time.Duration // How often to poll for pending messages (default: 1s)
	BatchSize    int           // Max messages per poll cycle (default: 100)
}

// Relay polls the outbox_messages table and forwards pending messages to Kafka.
// It uses SELECT … FOR UPDATE SKIP LOCKED for zero-contention concurrent access.
//
// TODO: CDC (Debezium) relay for real-time propagation.
type Relay struct {
	store  *Store
	writer *kafka.Writer
	logger *zap.Logger
	cfg    RelayConfig
}

// NewRelay creates a new polling relay.
func NewRelay(store *Store, writer *kafka.Writer, logger *zap.Logger, cfg RelayConfig) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: logger,
		cfg:    cfg,
	}
}

// Run starts the relay loop. Blocks until ctx is cancelled.
// When a full batch is fetched, it immediately polls again to drain backlogs.
// When fewer messages are returned, it sleeps for PollInterval before the next poll.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		zap.Duration("poll_interval", r.cfg.PollInterval),
		zap.Int("batch_size", r.cfg.BatchSize),
	)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		default:
		}

		processed, err := r.pollAndForward(ctx)
		if err != nil {
			r.logger.Error("outbox relay poll error", zap.Error(err))
		}

		// If we got a full batch, there might be more — poll immediately.
		if processed >= r.cfg.BatchSize {
			continue
		}

		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

// pollAndForward fetches pending messages, writes them to Kafka, and marks them as sent.
// Returns the number of messages successfully processed.
func (r *Relay) pollAndForward(ctx context.Context) (int, error) {
	messages, err := r.store.FetchPending(ctx, r.cfg.BatchSize)
	if err != nil {
		return 0, err
	}

	if len(messages) == 0 {
		return 0, nil
	}

	processed := 0

	for _, msg := range messages {
		kafkaMsg := toKafkaMessage(msg)

		if err := r.writer.WriteMessages(ctx, kafkaMsg); err != nil {
			r.logger.Error("outbox relay: kafka write failed",
				zap.String("message_id", msg.ID),
				zap.String("event_type", msg.EventType),
				zap.String("topic", msg.Topic),
				zap.Error(err),
			)
			// Continue to next message — failed ones stay pending for retry.
			continue
		}

		if err := r.store.MarkSent(ctx, msg.ID); err != nil {
			r.logger.Error("outbox relay: mark sent failed",
				zap.String("message_id", msg.ID),
				zap.Error(err),
			)
			continue
		}

		processed++
	}

	if processed > 0 {
		r.logger.Debug("outbox relay: forwarded messages",
			zap.Int("count", processed),
			zap.Int("total_fetched", len(messages)),
		)
	}

	return processed, nil
}

// toKafkaMessage converts an outbox Message to a kafka-go Message.
func toKafkaMessage(msg *Message) kafka.Message {
	headers := make([]kafka.Header, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, kafka.Header{
			Key:   k,
			Value: []byte(v),
		})
	}

	return kafka.Message{
		Topic:   msg.Topic,
		Key:     []byte(msg.Key),
		Value:   msg.Payload,
		Headers: headers,
	}
}
