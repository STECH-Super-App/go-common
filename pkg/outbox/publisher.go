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
	store           MessageInserter
	defaultTopic    string
	maxMessageBytes int
}

// DefaultMaxMessageBytes is the largest event PublishProto accepts: key, payload
// and headers together. Kafka's default record limit (message.max.bytes) and
// kafka-go's writer batch limit are both 1 MiB; this stays well under so the
// record framing and the growth of the payload when Postgres stores it as jsonb
// (it adds a space after every colon and comma) cannot push a row that passed
// the check over the broker's limit.
const DefaultMaxMessageBytes = 900_000

// PublisherOption tunes a Publisher.
type PublisherOption func(*Publisher)

// WithMaxMessageBytes sets the size limit PublishProto enforces (default
// DefaultMaxMessageBytes). Non-positive keeps the default. Raising it above the
// broker's limit only moves the failure from the business request to the relay.
func WithMaxMessageBytes(n int) PublisherOption {
	return func(p *Publisher) {
		if n > 0 {
			p.maxMessageBytes = n
		}
	}
}

// NewPublisher creates a new transactional event publisher.
// defaultTopic is the Kafka destination for all messages unless overridden per-call.
func NewPublisher(store MessageInserter, defaultTopic string, opts ...PublisherOption) *Publisher {
	p := &Publisher{store: store, defaultTopic: defaultTopic, maxMessageBytes: DefaultMaxMessageBytes}
	for _, o := range opts {
		o(p)
	}
	return p
}
