package outbox

import (
	"context"
	"errors"
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

// pollAndForward fetches pending messages, writes them to Kafka in a single
// batched call, and marks them all sent in a single UPDATE.
//
// Calling kafka.Writer.WriteMessages once per message blocks each call by the
// writer's BatchTimeout (1s default), which caps throughput at ~1 msg/s
// regardless of the configured BatchSize. Sending the whole slice in one call
// lets kafka-go batch them naturally and returns sub-second per poll for any
// realistic batch.
//
// Returns the number of messages successfully processed.
func (r *Relay) pollAndForward(ctx context.Context) (int, error) {
	messages, err := r.store.FetchPending(ctx, r.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	if len(messages) == 0 {
		return 0, nil
	}

	kafkaMsgs := make([]kafka.Message, len(messages))
	ids := make([]string, len(messages))
	for i, msg := range messages {
		kafkaMsgs[i] = toKafkaMessage(msg)
		ids[i] = msg.ID
	}

	if err := r.writer.WriteMessages(ctx, kafkaMsgs...); err != nil {
		// kafka-go returns either a single error (whole batch failed,
		// e.g., broker down) or kafka.WriteErrors with one entry per
		// message (partial failure). Mark only the entries that
		// succeeded; the rest stay pending and retry on the next poll.
		var werrs kafka.WriteErrors
		if errors.As(err, &werrs) {
			succeededIDs := make([]string, 0, len(messages))
			failed := 0
			for i, werr := range werrs {
				if werr == nil {
					succeededIDs = append(succeededIDs, ids[i])
					continue
				}
				failed++
				r.logger.Error("outbox relay: kafka write failed",
					zap.String("message_id", messages[i].ID),
					zap.String("event_type", messages[i].EventType),
					zap.String("topic", messages[i].Topic),
					zap.Error(werr),
				)
			}
			if len(succeededIDs) == 0 {
				return 0, nil
			}
			if err := r.store.MarkSentBatch(ctx, succeededIDs); err != nil {
				r.logger.Error("outbox relay: mark sent batch failed",
					zap.Int("count", len(succeededIDs)),
					zap.Error(err),
				)
				return 0, err
			}
			r.logger.Debug("outbox relay: partial forward",
				zap.Int("succeeded", len(succeededIDs)),
				zap.Int("failed", failed),
			)
			return len(succeededIDs), nil
		}

		r.logger.Error("outbox relay: kafka batch write failed",
			zap.Int("batch_size", len(messages)),
			zap.Error(err),
		)
		return 0, err
	}

	if err := r.store.MarkSentBatch(ctx, ids); err != nil {
		r.logger.Error("outbox relay: mark sent batch failed",
			zap.Int("count", len(ids)),
			zap.Error(err),
		)
		return 0, err
	}

	r.logger.Debug("outbox relay: forwarded messages",
		zap.Int("count", len(messages)),
	)
	return len(messages), nil
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
