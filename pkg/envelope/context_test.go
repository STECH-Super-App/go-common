package envelope_test

import (
	"context"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
)

func TestWithHeaders_roundTrip(t *testing.T) {
	h := envelope.Headers{
		envelope.HeaderEventID:   "id-1",
		envelope.HeaderEventType: "events.users.v1.UserUpdated",
	}
	ctx := envelope.WithHeaders(context.Background(), h)

	got, ok := envelope.HeadersFromContext(ctx)
	if !ok {
		t.Fatal("HeadersFromContext returned ok=false on a ctx that was set")
	}
	if got.EventID() != "id-1" {
		t.Errorf("EventID() = %q", got.EventID())
	}
	if got.EventType() != "events.users.v1.UserUpdated" {
		t.Errorf("EventType() = %q", got.EventType())
	}
}

func TestHeadersFromContext_empty(t *testing.T) {
	if _, ok := envelope.HeadersFromContext(context.Background()); ok {
		t.Error("expected ok=false on a ctx with no Headers")
	}
}

func TestHeadersFromContext_nil(t *testing.T) {
	//nolint:staticcheck // intentionally pass nil ctx to verify graceful behavior
	if _, ok := envelope.HeadersFromContext(nil); ok {
		t.Error("expected ok=false on nil ctx")
	}
}

func TestWithHeaders_nilCtx(t *testing.T) {
	h := envelope.Headers{envelope.HeaderEventID: "id-1"}
	//nolint:staticcheck // intentionally pass nil ctx
	ctx := envelope.WithHeaders(nil, h)
	if ctx == nil {
		t.Fatal("WithHeaders(nil, ...) returned nil ctx")
	}
	got, ok := envelope.HeadersFromContext(ctx)
	if !ok || got.EventID() != "id-1" {
		t.Errorf("nil-ctx fallback failed: ok=%v eventID=%q", ok, got.EventID())
	}
}
