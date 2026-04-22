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

	// HeaderOutboxID is the legacy header key written by pre-refactor
	// producers. Kept as a constant so consumers can reference it while the
	// rolling migration is in progress. Removed in the cleanup PR.
	// For reads, use ExtractEventID — it handles both keys.
	HeaderOutboxID = "outbox_id"

	HeaderEventType     = "event_type"
	HeaderAggregateType = "aggregate_type"
	HeaderAggregateID   = "aggregate_id"
	HeaderOccurredAt    = "occurred_at"
	HeaderSchemaVersion = "schema_version"
	HeaderContentType   = "content_type"
	HeaderRetryCount    = "x-retry-count"
)

// Values for well-known header keys.
const (
	ContentTypeProtoJSON = "application/protobuf+json"
	ContentTypeJSON      = "application/json" // legacy; emitted during transitional compat
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

func (h Headers) EventID() string       { return h[HeaderEventID] }
func (h Headers) EventType() string     { return h[HeaderEventType] }
func (h Headers) AggregateType() string { return h[HeaderAggregateType] }
func (h Headers) AggregateID() string   { return h[HeaderAggregateID] }
func (h Headers) SchemaVersion() string { return h[HeaderSchemaVersion] }
func (h Headers) ContentType() string   { return h[HeaderContentType] }

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

// ExtractEventID reads the event identifier from a raw Kafka header slice.
// Prefers HeaderEventID ("event_id"); falls back to the legacy literal
// "outbox_id" for messages produced before the events-to-proto refactor.
// Transitional compat — the fallback is removed in the cleanup PR.
func ExtractEventID(headers []kafka.Header) string {
	for _, h := range headers {
		if h.Key == HeaderEventID {
			return string(h.Value)
		}
	}
	for _, h := range headers {
		if h.Key == HeaderOutboxID {
			return string(h.Value)
		}
	}
	return ""
}

// ExtractOutboxID is a DEPRECATED alias of ExtractEventID kept for transitional
// compatibility during the events-to-proto refactor rollout. New callers should
// use ExtractEventID. Removed in the cleanup PR.
func ExtractOutboxID(headers []kafka.Header) string {
	return ExtractEventID(headers)
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
