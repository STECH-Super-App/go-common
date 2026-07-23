package notifyoutbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

// testTenantID is a fixed tenant UUID used by the tenant-addressed
// validation tests.
const testTenantID = "22222222-2222-2222-2222-222222222222"

// validEnvelope returns a minimally-valid IN_APP envelope. Tests mutate
// fields to drive specific validation paths.
func validEnvelope(t *testing.T) *notificationv1.NotificationEnvelope {
	t.Helper()
	return &notificationv1.NotificationEnvelope{
		Metadata: &notificationv1.EnvelopeMetadata{
			EventId:         "11111111-1111-1111-1111-111111111111",
			AggregateId:     "agg-1",
			OccurredAt:      timestamppb.New(time.Now()),
			RecipientUserId: "rcp-1",
			Locale:          "en",
			Type:            notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED,
			Channels:        []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP},
			DeepLink: &notificationv1.DeepLinkTarget{
				Screen: notificationv1.DeepLinkScreen_DEEP_LINK_SCREEN_LISTING_CARD,
				Params: map[string]string{"listing_id": "L1"},
			},
		},
		Payload: &notificationv1.NotificationEnvelope_SendListingApproved{
			SendListingApproved: &notificationv1.SendListingApproved{
				ListingId:    "L1",
				ListingTitle: "Test listing",
			},
		},
	}
}

// assertReason unwraps an AppError and asserts Reason matches want.
func assertReason(t *testing.T, err error, want string) {
	t.Helper()
	var appErr *commonerr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T: %v", err, err)
	}
	if appErr.Reason != want {
		t.Errorf("Reason = %q, want %q", appErr.Reason, want)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	if err := validate(validEnvelope(t)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidate_EmptyEventID(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.EventId = ""
	assertReason(t, validate(e), ReasonEmptyEventID)
}

func TestValidate_EmptyAggregateID(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.AggregateId = ""
	assertReason(t, validate(e), ReasonEmptyAggregateID)
}

func TestValidate_EmptyOccurredAt(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.OccurredAt = nil
	assertReason(t, validate(e), ReasonEmptyOccurredAt)
}

func TestValidate_EmptyLocale(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.Locale = ""
	assertReason(t, validate(e), ReasonEmptyLocale)
}

func TestValidate_UnspecifiedType(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.Type = notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED
	assertReason(t, validate(e), ReasonUnspecifiedType)
}

func TestValidate_EmptyChannels(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.Channels = nil
	assertReason(t, validate(e), ReasonEmptyChannels)
}

func TestValidate_EmptyRecipient(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	assertReason(t, validate(e), ReasonEmptyRecipient)
}

func TestValidate_NonInAppAllowsEmptyRecipient(t *testing.T) {
	cases := []struct {
		name     string
		channels []notificationv1.Channel
	}{
		{"SMS only", []notificationv1.Channel{notificationv1.Channel_CHANNEL_SMS}},
		{"EMAIL only", []notificationv1.Channel{notificationv1.Channel_CHANNEL_EMAIL}},
		{"SMS + EMAIL", []notificationv1.Channel{notificationv1.Channel_CHANNEL_SMS, notificationv1.Channel_CHANNEL_EMAIL}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			e := validEnvelope(t)
			e.Metadata.RecipientUserId = ""
			e.Metadata.Channels = tc.channels
			e.Metadata.DeepLink = nil // not required when IN_APP not in channels
			if err := validate(e); err != nil {
				t.Errorf("non-in-app with empty recipient should pass, got %v", err)
			}
		})
	}
}

// TestValidate_InAppRequiresRecipient confirms IN_APP still demands recipient_user_id.
func TestValidate_InAppRequiresRecipient(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	// channels already includes IN_APP in the default validEnvelope
	assertReason(t, validate(e), ReasonEmptyRecipient)
}

// TestValidate_PushRequiresRecipient confirms PUSH demands recipient_user_id.
func TestValidate_PushRequiresRecipient(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	e.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_PUSH}
	e.Metadata.DeepLink = nil
	assertReason(t, validate(e), ReasonEmptyRecipient)
}

// TestValidate_TenantAddressed: an IN_APP envelope addressed to a tenant
// (recipient_tenant_id set, recipient_user_id empty) is valid — the
// channel-owning consumer resolves it to the live member set at delivery time.
func TestValidate_TenantAddressed(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	e.Metadata.RecipientTenantId = testTenantID
	if err := validate(e); err != nil {
		t.Fatalf("tenant-addressed IN_APP must validate, got %v", err)
	}
}

// TestValidate_TenantAddressedWithRoleFilter: the role filter is optional
// delivery-time metadata and does not affect envelope validity.
func TestValidate_TenantAddressedWithRoleFilter(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	e.Metadata.RecipientTenantId = testTenantID
	e.Metadata.RecipientRoleFilter = []string{"ADMIN", "MANAGER"}
	if err := validate(e); err != nil {
		t.Fatalf("tenant-addressed with role filter must validate, got %v", err)
	}
}

// TestValidate_BothRecipientsRejected: setting BOTH recipient_user_id and
// recipient_tenant_id is ambiguous addressing — rejected regardless of channels.
func TestValidate_BothRecipientsRejected(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = "rcp-1"
	e.Metadata.RecipientTenantId = testTenantID
	assertReason(t, validate(e), ReasonAmbiguousRecipient)
}

// TestValidate_BothRecipientsRejectedSMS: the ambiguity check fires even on
// SMS-only channels, where neither recipient would otherwise be required.
func TestValidate_BothRecipientsRejectedSMS(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = "rcp-1"
	e.Metadata.RecipientTenantId = testTenantID
	e.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_SMS}
	e.Metadata.DeepLink = nil
	assertReason(t, validate(e), ReasonAmbiguousRecipient)
}

// TestValidate_TenantAddressedSMS: a tenant-addressed SMS-only envelope is
// valid (neither recipient required for SMS; recipient_tenant_id present).
func TestValidate_TenantAddressedSMS(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.RecipientUserId = ""
	e.Metadata.RecipientTenantId = testTenantID
	e.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_SMS}
	e.Metadata.DeepLink = nil
	if err := validate(e); err != nil {
		t.Fatalf("tenant-addressed SMS-only must validate, got %v", err)
	}
}

func TestValidate_MissingDeepLinkForInApp(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.DeepLink = nil
	assertReason(t, validate(e), ReasonMissingDeepLink)
}

func TestValidate_EmptyPayload(t *testing.T) {
	e := validEnvelope(t)
	e.Payload = nil
	assertReason(t, validate(e), ReasonEmptyPayload)
}

func TestValidateParams_MissingRequired(t *testing.T) {
	e := validEnvelope(t)
	// Replace payload with one that's missing listing_title (required for LISTING_APPROVED)
	e.Payload = &notificationv1.NotificationEnvelope_SendListingApproved{
		SendListingApproved: &notificationv1.SendListingApproved{ListingId: "L1"},
	}
	err := validateParams(e)
	assertReason(t, err, ReasonMissingParam)
	var appErr *commonerr.AppError
	if errors.As(err, &appErr) {
		if appErr.Params["param"] != "listing_title" {
			t.Errorf("params[param] = %v, want 'listing_title'", appErr.Params["param"])
		}
	}
}

// platformMessageEnvelope returns a valid verbatim PLATFORM_MESSAGE IN_APP
// envelope. Tests mutate the payload to drive the verbatim validation branch.
func platformMessageEnvelope(t *testing.T) *notificationv1.NotificationEnvelope {
	t.Helper()
	e := validEnvelope(t)
	e.Metadata.Type = notificationv1.NotificationType_NOTIFICATION_TYPE_PLATFORM_MESSAGE
	e.Metadata.DeepLink = &notificationv1.DeepLinkTarget{
		Screen: notificationv1.DeepLinkScreen_DEEP_LINK_SCREEN_TENANT,
	}
	e.Payload = &notificationv1.NotificationEnvelope_SendPlatformMessage{
		SendPlatformMessage: &notificationv1.SendPlatformMessage{
			Title: "Maintenance", Body: "Down at 02:00",
		},
	}
	return e
}

// TestValidateParams_VerbatimAccepted: a PLATFORM_MESSAGE with non-empty text
// passes validateParams even though it has no catalog/required-param contract.
func TestValidateParams_VerbatimAccepted(t *testing.T) {
	if err := validateParams(platformMessageEnvelope(t)); err != nil {
		t.Fatalf("expected verbatim PLATFORM_MESSAGE to pass, got %v", err)
	}
}

// TestValidateParams_VerbatimTitleOnly: a title with empty body is still valid.
func TestValidateParams_VerbatimTitleOnly(t *testing.T) {
	e := platformMessageEnvelope(t)
	e.Payload = &notificationv1.NotificationEnvelope_SendPlatformMessage{
		SendPlatformMessage: &notificationv1.SendPlatformMessage{Title: "Hi"},
	}
	if err := validateParams(e); err != nil {
		t.Errorf("title-only verbatim should pass, got %v", err)
	}
}

// TestValidateParams_VerbatimEmptyRejected: both title+body empty is rejected
// with the verbatim Reason, NOT the catalog missing-param Reason.
func TestValidateParams_VerbatimEmptyRejected(t *testing.T) {
	e := platformMessageEnvelope(t)
	e.Payload = &notificationv1.NotificationEnvelope_SendPlatformMessage{
		SendPlatformMessage: &notificationv1.SendPlatformMessage{},
	}
	assertReason(t, validateParams(e), ReasonEmptyVerbatim)
}

// TestPublishDirective_VerbatimWritesRow: full publish path for a valid
// PLATFORM_MESSAGE writes exactly one outbox row.
func TestPublishDirective_VerbatimWritesRow(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "default-topic")
	if err := PublishDirective(context.Background(), pub, nil, platformMessageEnvelope(t)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if got := len(store.Messages); got != 1 {
		t.Fatalf("expected 1 outbox row, got %d", got)
	}
}

func TestPublishDirective_NilPublisher(t *testing.T) {
	err := PublishDirective(context.Background(), nil, nil, validEnvelope(t))
	assertReason(t, err, ReasonNilPublisher)
}

func TestPublishDirective_RejectsBeforeOutbox(t *testing.T) {
	// Verifies validation failures short-circuit before any outbox write.
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "default-topic")
	e := validEnvelope(t)
	e.Metadata.EventId = ""
	err := PublishDirective(context.Background(), pub, nil, e)
	assertReason(t, err, ReasonEmptyEventID)
	if got := len(store.Messages); got != 0 {
		t.Errorf("expected no outbox write, got %d", got)
	}
}

func TestPublishDirective_HappyWritesRow(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "default-topic")
	if err := PublishDirective(context.Background(), pub, nil, validEnvelope(t)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if got := len(store.Messages); got != 1 {
		t.Fatalf("expected 1 outbox row, got %d", got)
	}
	row := store.Messages[0]
	// Pin to the dotted wire name (not enum.String() which returns
	// "TOPIC_NOTIFICATION_EVENTS"). The outbox relay uses this field
	// directly as the Kafka topic; consumers subscribe to the dotted name.
	if row.Topic != "notification.events" {
		t.Errorf("Topic = %q, want %q", row.Topic, "notification.events")
	}
	if row.AggregateType != AggregateType {
		t.Errorf("AggregateType = %q, want %q", row.AggregateType, AggregateType)
	}
	if row.AggregateID != "agg-1" {
		t.Errorf("AggregateID = %q, want agg-1", row.AggregateID)
	}
	if row.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("Message.ID = %q, want envelope EventId", row.ID)
	}
}
