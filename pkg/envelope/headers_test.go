package envelope_test

import (
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
)

func TestHeaderConstants(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{envelope.HeaderEventID, "event_id"},
		{envelope.HeaderEventType, "event_type"},
		{envelope.HeaderAggregateType, "aggregate_type"},
		{envelope.HeaderAggregateID, "aggregate_id"},
		{envelope.HeaderOccurredAt, "occurred_at"},
		{envelope.HeaderSchemaVersion, "schema_version"},
		{envelope.HeaderContentType, "content_type"},
		{envelope.HeaderRetryCount, "x-retry-count"},
		{envelope.ContentTypeProtoJSON, "application/protobuf+json"},
		{envelope.SchemaVersionV1, "v1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

const testUserRegisteredFQN = "events.users.v1.UserRegistered"

func TestHeaders_accessors(t *testing.T) {
	occurred := "2026-04-22T10:00:00.123456Z"
	h := envelope.Headers{
		envelope.HeaderEventID:       "id-1",
		envelope.HeaderEventType:     testUserRegisteredFQN,
		envelope.HeaderAggregateType: "user",
		envelope.HeaderAggregateID:   "user-1",
		envelope.HeaderOccurredAt:    occurred,
		envelope.HeaderSchemaVersion: "v1",
		envelope.HeaderContentType:   "application/protobuf+json",
		envelope.HeaderRetryCount:    "2",
	}
	if got := h.EventID(); got != "id-1" {
		t.Errorf("EventID() = %q", got)
	}
	if got := h.EventType(); got != testUserRegisteredFQN {
		t.Errorf("EventType() = %q", got)
	}
	if got := h.AggregateType(); got != "user" {
		t.Errorf("AggregateType() = %q", got)
	}
	if got := h.AggregateID(); got != "user-1" {
		t.Errorf("AggregateID() = %q", got)
	}
	ts, err := h.OccurredAt()
	if err != nil {
		t.Fatalf("OccurredAt() err = %v", err)
	}
	want, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Equal(want) {
		t.Errorf("OccurredAt() = %v, want %v", ts, want)
	}
	if got := h.SchemaVersion(); got != "v1" {
		t.Errorf("SchemaVersion() = %q", got)
	}
	if got := h.ContentType(); got != "application/protobuf+json" {
		t.Errorf("ContentType() = %q", got)
	}
	if got := h.RetryCount(); got != 2 {
		t.Errorf("RetryCount() = %d", got)
	}
}

func TestHeaders_missingRetryCount_returnsZero(t *testing.T) {
	if got := (envelope.Headers{}).RetryCount(); got != 0 {
		t.Errorf("RetryCount() on empty headers = %d, want 0", got)
	}
}

func TestHeaders_invalidOccurredAt_returnsError(t *testing.T) {
	h := envelope.Headers{envelope.HeaderOccurredAt: "not-a-timestamp"}
	if _, err := h.OccurredAt(); err == nil {
		t.Fatal("OccurredAt() err = nil, want error")
	}
}

func TestFromKafka_roundTrip(t *testing.T) {
	raw := []kafka.Header{
		{Key: envelope.HeaderEventID, Value: []byte("e1")},
		{Key: envelope.HeaderEventType, Value: []byte("events.users.v1.UserRegistered")},
	}
	h := envelope.FromKafka(raw)
	if got := h.EventID(); got != "e1" {
		t.Errorf("EventID() = %q", got)
	}
	if got := h.EventType(); got != "events.users.v1.UserRegistered" {
		t.Errorf("EventType() = %q", got)
	}
}

func TestExtractEventID_newHeader(t *testing.T) {
	h := []kafka.Header{{Key: envelope.HeaderEventID, Value: []byte("new-id")}}
	if got := envelope.ExtractEventID(h); got != "new-id" {
		t.Errorf("ExtractEventID = %q, want %q", got, "new-id")
	}
}

func TestExtractEventID_missing_returnsEmpty(t *testing.T) {
	if got := envelope.ExtractEventID(nil); got != "" {
		t.Errorf("ExtractEventID(nil) = %q, want empty", got)
	}
}

func TestExtractEventType(t *testing.T) {
	h := []kafka.Header{{Key: envelope.HeaderEventType, Value: []byte(testUserRegisteredFQN)}}
	if got := envelope.ExtractEventType(h); got != testUserRegisteredFQN {
		t.Errorf("ExtractEventType = %q", got)
	}
}
