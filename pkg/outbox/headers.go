package outbox

import "github.com/segmentio/kafka-go"

// ExtractOutboxID reads the outbox_id header from a Kafka message.
// Returns empty string if the header is not present (e.g., legacy messages).
func ExtractOutboxID(headers []kafka.Header) string {
	for _, h := range headers {
		if h.Key == "outbox_id" {
			return string(h.Value)
		}
	}
	return ""
}

// ExtractEventType reads the event_type header from a Kafka message.
// Returns empty string if the header is not present.
func ExtractEventType(headers []kafka.Header) string {
	for _, h := range headers {
		if h.Key == "event_type" {
			return string(h.Value)
		}
	}
	return ""
}
