package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Publisher provides transactional event publishing via the outbox pattern.
// It is the primary API for upstream services — call Publish() inside a
// RunInTx block to atomically write both the domain mutation and the event.
//
// The defaultTopic is applied when PublishOptions.Topic is empty, keeping
// Kafka routing out of the application layer (infrastructure concern).
type Publisher struct {
	store        *Store
	defaultTopic string
}

// NewPublisher creates a new transactional event publisher.
// defaultTopic is the Kafka destination for all messages unless overridden per-call.
func NewPublisher(store *Store, defaultTopic string) *Publisher {
	return &Publisher{store: store, defaultTopic: defaultTopic}
}

// PublishOptions configures a single outbox message to be published.
type PublishOptions struct {
	AggregateType string            // Domain aggregate, e.g. "user"
	AggregateID   string            // Unique ID within the aggregate
	EventType     string            // Dot-delimited event, e.g. "user.created"
	Topic         string            // Kafka destination topic
	Payload       any               // Will be JSON-marshaled
	Headers       map[string]string // Optional extra Kafka headers
}

// Publish inserts an outbox message within the given transaction.
// The caller must commit the transaction for the message to persist.
//
// Automatically injects:
//   - outbox_id header for consumer-side idempotency/deduplication
//   - event_type header matching EventType
//   - AggregateID as the Kafka partition key
func (p *Publisher) Publish(ctx context.Context, tx pgx.Tx, opts PublishOptions) error {
	payload, err := json.Marshal(opts.Payload)
	if err != nil {
		return fmt.Errorf("outbox: marshal payload: %w", err)
	}

	messageID := uuid.New().String()

	headers := make(map[string]string, len(opts.Headers)+2)
	for k, v := range opts.Headers {
		headers[k] = v
	}
	headers["outbox_id"] = messageID
	headers["event_type"] = opts.EventType

	msg := &Message{
		ID:            messageID,
		AggregateType: opts.AggregateType,
		AggregateID:   opts.AggregateID,
		EventType:     opts.EventType,
		Topic:         p.resolveTopic(opts.Topic),
		Key:           opts.AggregateID,
		Payload:       payload,
		Headers:       headers,
		Status:        StatusPending,
		CreatedAt:     time.Now().UTC(),
	}

	return p.store.InsertTx(ctx, tx, msg)
}

// resolveTopic returns the per-call topic if set, otherwise the default.
func (p *Publisher) resolveTopic(perCall string) string {
	if perCall != "" {
		return perCall
	}
	return p.defaultTopic
}
