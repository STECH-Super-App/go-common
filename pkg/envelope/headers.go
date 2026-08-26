// Package envelope defines the Kafka wire contract between event producers
// (pkg/outbox) and event consumers (pkg/events). It is the single source of
// truth for envelope header keys, value conventions, and parsing helpers —
// both sides of the pipeline import this package so the contract cannot
// drift between producer and consumer.
//
// See design spec §4.2 for the full envelope semantics.
package envelope

import (
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
)

// Kafka header keys used on every event message.
const (
	// HeaderEventID is the UUID identity of the event. Mirrors the event_id
	// field carried in the proto payload.
	HeaderEventID = "event_id"

	HeaderEventType     = "event_type"
	HeaderAggregateType = "aggregate_type"
	HeaderAggregateID   = "aggregate_id"
	HeaderOccurredAt    = "occurred_at"
	HeaderSchemaVersion = "schema_version"
	HeaderContentType   = "content_type"
	HeaderRetryCount    = "x-retry-count"

	// HeaderTraceparent carries the W3C trace context across the async hop, so
	// one trace spans request → outbox → relay → consumer. It is injected by
	// outbox.Publisher.PublishProto from the PRODUCING request's context
	// (never by the relay, which would root every event's trace at the relay
	// process) and extracted by the events Dispatcher as a remote parent.
	//
	// Lowercase by W3C definition, unlike the x-dlq-* diagnostics.
	HeaderTraceparent = "traceparent"
)

// Values for well-known header keys.
const (
	ContentTypeProtoJSON = "application/protobuf+json"
	SchemaVersionV1      = "v1"
)

// Headers is a typed view over a Kafka message's envelope headers.
// Produced by FromKafka; read via the typed accessors below.
type Headers map[string]string

// FromKafka builds a Headers view from raw kafka.Header entries.
func FromKafka(headers []kafka.Header) Headers {
	out := make(Headers, len(headers))
	for _, h := range headers {
		out[h.Key] = string(h.Value)
	}
	return out
}

// EventID returns the event_id header value (UUID of the event).
func (h Headers) EventID() string { return h[HeaderEventID] }

// EventType returns the event_type header value (proto fully-qualified name).
func (h Headers) EventType() string { return h[HeaderEventType] }

// AggregateType returns the aggregate_type header value (domain aggregate name).
func (h Headers) AggregateType() string { return h[HeaderAggregateType] }

// AggregateID returns the aggregate_id header value (the aggregate's unique id).
func (h Headers) AggregateID() string { return h[HeaderAggregateID] }

// SchemaVersion returns the schema_version header value (e.g. "v1").
func (h Headers) SchemaVersion() string { return h[HeaderSchemaVersion] }

// ContentType returns the content_type header value (e.g. application/protobuf+json).
func (h Headers) ContentType() string { return h[HeaderContentType] }

// Traceparent returns the W3C traceparent header value, or empty when the
// message was produced without an active trace.
func (h Headers) Traceparent() string { return h[HeaderTraceparent] }

// OccurredAt parses the occurred_at header as RFC3339Nano UTC.
// Returns an error if the header is missing or malformed.
func (h Headers) OccurredAt() (time.Time, error) {
	return time.Parse(time.RFC3339Nano, h[HeaderOccurredAt])
}

// RetryCount returns the value of x-retry-count or 0 if absent/unparseable.
func (h Headers) RetryCount() int {
	n, err := strconv.Atoi(h[HeaderRetryCount])
	if err != nil {
		return 0
	}
	return n
}

// ExtractEventID reads the event_id header from a raw Kafka header slice.
// Returns empty string if not present.
func ExtractEventID(headers []kafka.Header) string {
	for _, h := range headers {
		if h.Key == HeaderEventID {
			return string(h.Value)
		}
	}
	return ""
}

// ExtractEventType reads the event_type header from a raw Kafka header slice.
// Returns empty string if not present.
func ExtractEventType(headers []kafka.Header) string {
	for _, h := range headers {
		if h.Key == HeaderEventType {
			return string(h.Value)
		}
	}
	return ""
}
