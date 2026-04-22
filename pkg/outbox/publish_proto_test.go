package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

func TestPublishProto_injectsAllEnvelopeHeaders(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user-events")

	evt := &usersv1.UserRegistered{UserId: "u1", Name: "Alice"}
	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       evt,
	})
	if err != nil {
		t.Fatalf("PublishProto: %v", err)
	}

	if len(store.Messages) != 1 {
		t.Fatalf("got %d rows, want 1", len(store.Messages))
	}
	row := store.Messages[0]

	h := row.Headers
	if _, err := uuid.Parse(h[envelope.HeaderEventID]); err != nil {
		t.Errorf("HeaderEventID not a UUID: %q", h[envelope.HeaderEventID])
	}
	if got := h[envelope.HeaderEventType]; got != "events.users.v1.UserRegistered" {
		t.Errorf("HeaderEventType = %q, want %q", got, "events.users.v1.UserRegistered")
	}
	if got := h[envelope.HeaderAggregateType]; got != "user" {
		t.Errorf("HeaderAggregateType = %q", got)
	}
	if got := h[envelope.HeaderAggregateID]; got != "u1" {
		t.Errorf("HeaderAggregateID = %q", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, h[envelope.HeaderOccurredAt]); err != nil {
		t.Errorf("HeaderOccurredAt not RFC3339Nano: %q", h[envelope.HeaderOccurredAt])
	}
	if got := h[envelope.HeaderSchemaVersion]; got != envelope.SchemaVersionV1 {
		t.Errorf("HeaderSchemaVersion = %q", got)
	}
	if got := h[envelope.HeaderContentType]; got != envelope.ContentTypeProtoJSON {
		t.Errorf("HeaderContentType = %q", got)
	}

	// Payload round-trips as proto with UseProtoNames (snake_case fields).
	out := &usersv1.UserRegistered{}
	if err := protojson.Unmarshal(row.Payload, out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if out.UserId != "u1" || out.Name != "Alice" {
		t.Errorf("round-trip mismatch: %+v", out)
	}

	// event_id is written to both header and payload field, and they match.
	if out.EventId != h[envelope.HeaderEventID] {
		t.Errorf("payload event_id %q != header %q", out.EventId, h[envelope.HeaderEventID])
	}

	// Message row metadata.
	if row.ID != h[envelope.HeaderEventID] {
		t.Errorf("Message.ID %q != header event_id %q", row.ID, h[envelope.HeaderEventID])
	}
	if row.AggregateType != "user" || row.AggregateID != "u1" {
		t.Errorf("row aggregate fields wrong: %+v", row)
	}
	if row.Key != "u1" {
		t.Errorf("row.Key = %q, want partition key = aggregate_id %q", row.Key, "u1")
	}
	if row.EventType != "events.users.v1.UserRegistered" {
		t.Errorf("row.EventType = %q", row.EventType)
	}
	if row.Topic != "user-events" {
		t.Errorf("row.Topic = %q, want default", row.Topic)
	}
	_ = proto.Marshal // keep proto import
}

func TestPublishProto_explicitEventIDUsed(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user-events")

	explicit := uuid.NewString()
	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1"},
		EventID:       explicit,
	})
	if err != nil {
		t.Fatalf("PublishProto: %v", err)
	}
	h := store.Messages[0].Headers
	if h[envelope.HeaderEventID] != explicit {
		t.Errorf("HeaderEventID = %q, want explicit %q", h[envelope.HeaderEventID], explicit)
	}
	out := &usersv1.UserRegistered{}
	_ = protojson.Unmarshal(store.Messages[0].Payload, out)
	if out.EventId != explicit {
		t.Errorf("payload event_id = %q, want %q", out.EventId, explicit)
	}
}

func TestPublishProto_customTopicOverride(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user-events")
	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       &usersv1.UserRegistered{UserId: "u1"},
		Topic:         "custom-topic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.Messages[0].Topic != "custom-topic" {
		t.Errorf("Topic = %q, want custom-topic", store.Messages[0].Topic)
	}
}

func TestPublishProto_nilMessageReturnsError(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user-events")
	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user",
		AggregateID:   "u1",
		Message:       nil,
	})
	if err == nil {
		t.Fatal("err = nil, want error on nil Message")
	}
}
