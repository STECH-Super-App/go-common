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

// relayStore is the slice of *Store the relay actually uses. It exists so the
// poll loop — and the metric wiring hanging off its outcomes — can be unit
// tested without Postgres. *Store satisfies it.
type relayStore interface {
	FetchPending(ctx context.Context, batchSize int) ([]*Message, error)
	MarkSentBatch(ctx context.Context, ids []string) error
}

// relayWriter is the slice of *kafka.Writer the relay actually uses, for the
// same reason. *kafka.Writer satisfies it.
type relayWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Relay polls the outbox_messages table and forwards pending messages to Kafka.
// It uses SELECT … FOR UPDATE SKIP LOCKED for zero-contention concurrent access.
//
// TODO: CDC (Debezium) relay for real-time propagation.
type Relay struct {
	store  relayStore
	writer relayWriter
	logger *zap.Logger
	cfg    RelayConfig
}

// NewRelay creates a new polling relay. The concrete parameter types are kept
// so every existing call site compiles unchanged; the fields behind them are
// interfaces purely for testability.
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

	// Seed the freshness gauge at start: an unset Prometheus gauge reads 0,
	// which would make the "relay stalled" alert fire on every fresh deploy
	// before the first poll completes.
	recordRelaySuccess(0)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		default:
		}

		processed, err := r.pollAndForward(ctx)
		if err != nil {
			recordRelayError()
			r.logger.Error("outbox relay poll error", zap.Error(err))
		} else {
			// Error-free means successful, whether or not any row was found.
			recordRelaySuccess(processed)
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
				// EVERY message in the batch failed. Propagate the error
				// instead of reporting an error-free poll: returning nil here
				// would refresh outbox_last_success_timestamp_seconds and leave
				// outbox_relay_errors_total flat while zero messages were
				// delivered — a wedged relay that looks perfectly healthy on
				// every panel. That is exactly the shape a missing Kafka topic
				// produces (Critical Rule 13 — Kafka topic naming and
				// provisioning), which is a live condition in prod.
				//
				// Return semantics for the caller are unchanged: the rows stay
				// pending and the next poll retries them, as before.
				return 0, err
			}
			if failed > 0 {
				// Partial delivery. The succeeded half is real progress and is
				// recorded by the caller off the nil error; the failed half is
				// a genuine error and must move the error counter, or a relay
				// losing a steady fraction of every batch reports as clean.
				recordRelayError()
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
