package outbox

import "context"

// MessageInserter abstracts the write side of outbox storage.
// Implemented by *Store (Postgres) and *TestStore (in-memory).
//
// Accepts the opaque outbox.Tx so application callers never need to import
// a concrete transaction type.
type MessageInserter interface {
	InsertTx(ctx context.Context, tx Tx, msg *Message) error
}

// Publisher provides transactional event publishing via the outbox pattern.
// The new API is PublishProto (see publish_proto.go) — the JSON-payload
// `Publish` method that used to live here was removed in the events-to-proto
// cleanup. All callers now publish typed proto messages.
//
// The defaultTopic is applied when PublishProtoOptions.Topic is empty,
// keeping Kafka routing out of the application layer (infrastructure concern).
type Publisher struct {
	store        MessageInserter
	defaultTopic string
}

// NewPublisher creates a new transactional event publisher.
// defaultTopic is the Kafka destination for all messages unless overridden per-call.
func NewPublisher(store MessageInserter, defaultTopic string) *Publisher {
	return &Publisher{store: store, defaultTopic: defaultTopic}
}
