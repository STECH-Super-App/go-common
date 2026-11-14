package outbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/errors"
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
		return internalError(ReasonPublishInvalid, "outbox: PublishProto: Message is nil", nil)
	}

	eventID := opts.EventID
	if eventID == "" {
		eventID = uuid.NewString()
	}
	setEventIDField(opts.Message, eventID)

	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(opts.Message)
	if err != nil {
		return internalError(ReasonMarshalFailed, "outbox: marshal proto", err)
	}

	topic := opts.Topic
	if topic == "" {
		topic = p.defaultTopic
	}
	eventType := string(proto.MessageName(opts.Message))
	occurred := time.Now().UTC().Format(time.RFC3339Nano)

	headers := make(map[string]string, len(opts.Headers)+7)
	for k, v := range opts.Headers {
		// Baggage is caller-controlled data, never an event header (SL-24).
		if strings.EqualFold(k, "baggage") {
			continue
		}
		headers[k] = v
	}
	headers[envelope.HeaderEventID] = eventID
	headers[envelope.HeaderEventType] = eventType
	headers[envelope.HeaderAggregateType] = opts.AggregateType
	headers[envelope.HeaderAggregateID] = opts.AggregateID
	headers[envelope.HeaderOccurredAt] = occurred
	headers[envelope.HeaderSchemaVersion] = envelope.SchemaVersionV1
	headers[envelope.HeaderContentType] = envelope.ContentTypeProtoJSON

	// Capture the trace context HERE — at insert time, in the producing
	// request's context — not at relay time. The relay stamping its own
	// context would root every event's trace at the relay process:
	// technically a trace, semantically garbage (trap §9.17). The relay is
	// already transparent to it (toKafkaMessage copies every header entry
	// verbatim), and the consumer picks it up as a remote parent.
	//
	// Injects nothing when no span is active, so this is a no-op for services
	// that never call tracing.Init.
	//
	// Only the W3C trace context is written — never the global propagator,
	// which may include Baggage: a client-sent baggage header would otherwise
	// be copied into every row and Kafka message the request causes (SL-24).
	propagation.TraceContext{}.Inject(ctx, propagation.MapCarrier(headers))

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
	// Refused HERE, in the request that built the event: a row bigger than the
	// broker accepts can never be sent, and (SL-03) used to stall the whole outbox
	// behind it. The compact size is a lower bound, so this check is free and
	// exact for what it refuses; the Store then measures the size Postgres will
	// really send (jsonb::text) and refuses the rest in the same statement.
	if size := msg.compactWireSize(); size > p.maxMessageBytes {
		return eventTooLarge(eventType, size, p.maxMessageBytes)
	}
	if li, ok := p.store.(limitedInserter); ok {
		return li.InsertTxLimited(ctx, tx, msg, p.maxMessageBytes)
	}
	return p.store.InsertTx(ctx, tx, msg)
}

// eventTooLarge is the typed 422 of an event over the size limit.
func eventTooLarge(eventType string, size, limit int) error {
	return errors.New(http.StatusUnprocessableEntity).
		Reason(ReasonEventTooLarge).
		Message(fmt.Sprintf("outbox: %s event is %d bytes, over the %d byte limit", eventType, size, limit)).
		Params(map[string]any{"event_type": eventType, "size": size, "limit": limit}).
		Build()
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
