package errors_test

import (
	"errors"
	"net/http"
	"testing"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

func TestBuilderProducesAppErrorWithCodeOnly(t *testing.T) {
	cause := errors.New("boom")
	got := commonErrors.New(http.StatusBadRequest).Cause(cause).Build()

	if got.Code != http.StatusBadRequest {
		t.Fatalf("Code: want 400, got %d", got.Code)
	}
	if got.Reason != "" {
		t.Fatalf("Reason: want empty, got %q", got.Reason)
	}
	if got.Message != "" {
		t.Fatalf("Message: want empty, got %q", got.Message)
	}
	if got.Params != nil {
		t.Fatalf("Params: want nil, got %v", got.Params)
	}
	if got.Details != nil {
		t.Fatalf("Details: want nil, got %v", got.Details)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("errors.Is(got, cause) = false, want true")
	}
}

func TestBuilderFullChain(t *testing.T) {
	cause := errors.New("db down")
	got := commonErrors.New(http.StatusGone).
		Reason("TENANT_ADMIN_TRANSFER_EXPIRED").
		Message("admin transfer expired").
		Params(map[string]any{"expiry_time": "2026-04-28T14:00:00Z", "transfer_id": "abc"}).
		Cause(cause).
		Build()

	if got.Code != http.StatusGone {
		t.Errorf("Code: want 410, got %d", got.Code)
	}
	if got.Reason != "TENANT_ADMIN_TRANSFER_EXPIRED" {
		t.Errorf("Reason: want TENANT_ADMIN_TRANSFER_EXPIRED, got %q", got.Reason)
	}
	if got.Message != "admin transfer expired" {
		t.Errorf("Message: want 'admin transfer expired', got %q", got.Message)
	}
	if got.Params["expiry_time"] != "2026-04-28T14:00:00Z" {
		t.Errorf("Params expiry_time: want '2026-04-28T14:00:00Z', got %v", got.Params["expiry_time"])
	}
	if !errors.Is(got, cause) {
		t.Errorf("errors.Is(got, cause) = false, want true")
	}
}

func TestBuilderWithDetails(t *testing.T) {
	got := commonErrors.New(http.StatusBadRequest).
		Reason("TENANT_VALIDATION_FAILED").
		Message("validation failed").
		Details([]commonErrors.FieldError{
			{Field: "inn", Reason: "TENANT_INN_INVALID_LENGTH", Message: "invalid INN length", Params: map[string]any{"expected": 12}},
			{Field: "name", Reason: "TENANT_NAME_REQUIRED", Message: "name required"},
		}).
		Build()

	if len(got.Details) != 2 {
		t.Fatalf("Details: want 2 entries, got %d", len(got.Details))
	}
	if got.Details[0].Field != "inn" {
		t.Errorf("Details[0].Field: want 'inn', got %q", got.Details[0].Field)
	}
	if got.Details[1].Reason != "TENANT_NAME_REQUIRED" {
		t.Errorf("Details[1].Reason: want TENANT_NAME_REQUIRED, got %q", got.Details[1].Reason)
	}
}

func TestAppErrorErrorMessageFormatting(t *testing.T) {
	cause := errors.New("connection refused")
	withCause := commonErrors.New(http.StatusInternalServerError).Message("db unreachable").Cause(cause).Build()
	if got := withCause.Error(); got != "db unreachable: connection refused" {
		t.Errorf("Error() with cause: want 'db unreachable: connection refused', got %q", got)
	}
	noCause := commonErrors.New(http.StatusBadRequest).Message("bad request").Build()
	if got := noCause.Error(); got != "bad request" {
		t.Errorf("Error() no cause: want 'bad request', got %q", got)
	}
}
