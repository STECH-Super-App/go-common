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

// limitedInserter is the optional extra of a store that can measure the size
// of the row it writes exactly (*Store, in Postgres). TestStore does not have
// it: it keeps the compact payload and has no jsonb form to measure.
type limitedInserter interface {
	InsertTxLimited(ctx context.Context, tx Tx, msg *Message, maxWireBytes int) error
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
// (measured as Postgres will send it, jsonb::text) and headers together. Kafka's
// default record limit (message.max.bytes) and kafka-go's writer batch limit
// (BatchBytes) are both 1 MiB; this stays under so the record framing around the
// measured bytes (a few dozen bytes per record, per-header length prefixes)
// cannot push a row that passed the check over that limit.
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
