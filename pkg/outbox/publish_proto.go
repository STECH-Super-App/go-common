package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
)

// PublishProtoOptions configures a single typed-proto outbox publish.
// PublishProto is the only producer API — the legacy any-payload Publish was
// removed in the events-to-proto cleanup.
type PublishProtoOptions struct {
	AggregateType string
	AggregateID   string
	// Message is the proto event to publish. Its "event_id" field (if present,
	// and if EventID is not set below) will be populated by the publisher.
	Message proto.Message
	// Topic overrides the publisher's default topic. Empty means default.
	Topic string
	// EventID sets the event UUID explicitly. Empty means the publisher
	// generates a UUID and writes it both to the event_id header and to the
	// event_id field of the proto payload (if present).
	EventID string
	// Headers are additional Kafka headers merged with the envelope.
	// Envelope keys (event_id, event_type, etc.) cannot be overridden here.
	Headers map[string]string
}

// PublishProto inserts an outbox row for a proto message, serializing via
// protojson (UseProtoNames = snake_case fields) and auto-injecting the full
// envelope header set per design §4.2. This is the only producer API.
func (p *Publisher) PublishProto(ctx context.Context, tx Tx, opts PublishProtoOptions) error {
	if opts.Message == nil {
		return fmt.Errorf("outbox: PublishProto: Message is nil")
	}

	eventID := opts.EventID
	if eventID == "" {
		eventID = uuid.NewString()
	}
	setEventIDField(opts.Message, eventID)

	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(opts.Message)
	if err != nil {
		return fmt.Errorf("outbox: marshal proto: %w", err)
	}

	topic := opts.Topic
	if topic == "" {
		topic = p.defaultTopic
	}
	eventType := string(proto.MessageName(opts.Message))
	occurred := time.Now().UTC().Format(time.RFC3339Nano)

	headers := make(map[string]string, len(opts.Headers)+7)
	for k, v := range opts.Headers {
		headers[k] = v
	}
	headers[envelope.HeaderEventID] = eventID
	headers[envelope.HeaderEventType] = eventType
	headers[envelope.HeaderAggregateType] = opts.AggregateType
	headers[envelope.HeaderAggregateID] = opts.AggregateID
	headers[envelope.HeaderOccurredAt] = occurred
	headers[envelope.HeaderSchemaVersion] = envelope.SchemaVersionV1
	headers[envelope.HeaderContentType] = envelope.ContentTypeProtoJSON

	msg := &Message{
		ID:            eventID,
		AggregateType: opts.AggregateType,
		AggregateID:   opts.AggregateID,
		EventType:     eventType,
		Topic:         topic,
		Key:           opts.AggregateID,
		Payload:       payload,
		Headers:       headers,
		Status:        StatusPending,
		CreatedAt:     time.Now().UTC(),
	}
	return p.store.InsertTx(ctx, tx, msg)
}

// setEventIDField sets the "event_id" string field on msg by name, if present.
// No-op if the message has no such field or if the field is not a string.
func setEventIDField(msg proto.Message, id string) {
	r := msg.ProtoReflect()
	fd := r.Descriptor().Fields().ByName(protoreflect.Name("event_id"))
	if fd == nil {
		return
	}
	if fd.Kind() != protoreflect.StringKind {
		return
	}
	r.Set(fd, protoreflect.ValueOfString(id))
}
