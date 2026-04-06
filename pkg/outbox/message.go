// Package outbox implements the Transactional Outbox Pattern.
//
// It guarantees at-least-once event delivery by writing domain events into a
// Postgres outbox_messages table within the same transaction as the domain
// mutation. A background relay polls the table and forwards messages to Kafka.
//
// TODO: CDC (Debezium) relay for real-time propagation.
package outbox

import "time"

// Status represents the lifecycle state of an outbox message.
type Status string

const (
	// StatusPending indicates the message has been written but not yet forwarded.
	StatusPending Status = "pending"
	// StatusSent indicates the message has been successfully published to Kafka.
	StatusSent Status = "sent"
)

// Message represents a single outbox entry to be relayed to Kafka.
// Each message maps 1:1 to a row in the outbox_messages table.
type Message struct {
	ID            string
	AggregateType string            // Domain aggregate name, e.g. "user", "tenant"
	AggregateID   string            // Unique ID within the aggregate, e.g. user UUID
	EventType     string            // Dot-delimited event name, e.g. "user.created"
	Topic         string            // Kafka destination topic, e.g. "user-events"
	Key           string            // Kafka partition key (typically aggregate_id)
	Payload       []byte            // JSON-serialized event body
	Headers       map[string]string // Additional Kafka headers
	Status        Status
	CreatedAt     time.Time
	SentAt        *time.Time
}
