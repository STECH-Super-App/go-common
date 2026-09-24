package notifyoutbox

import (
	"context"
	"strings"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"

	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/notifyrender"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

// AggregateType is the aggregate-type tag stamped on every notification
// outbox row. The downstream consumer doesn't key on it, but it keeps
// the row introspectable in admin tooling.
const AggregateType = "notification"

// topicName is the canonical Kafka topic for notification directives.
// Held as a string for outbox.PublishProtoOptions.Topic. Uses
// events.TopicName to derive the dotted wire name ("notification.events")
// rather than enum.String() which returns "TOPIC_NOTIFICATION_EVENTS".
var topicName = events.TopicName(eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS)

// PublishDirective validates the envelope and writes it to the outbox
// via pub, addressed to TOPIC_NOTIFICATION_EVENTS. Producers must use
// this instead of pub.PublishProto for that topic — it enforces the
// per-channel validation rules the consumers rely on.
//
// The publisher is passed in (rather than constructed package-internally)
// so callers keep ownership of the outbox.Store lifecycle. The opaque
// outbox.Tx threads the same transaction the caller is mutating in.
//
// All errors are typed *AppError with a stable Reason code so the
// frontend's localization layer can render them; assert via
// errors.As(err, &*commonerr.AppError) and switch on appErr.Reason.
func PublishDirective(
	ctx context.Context,
	pub *outbox.Publisher,
	tx outbox.Tx,
	env *notificationv1.NotificationEnvelope,
) error {
	if pub == nil {
		return errNilPublisher()
	}
	if err := validate(env); err != nil {
		return err
	}
	if err := validateRepairChannels(env.GetMetadata()); err != nil {
		return err
	}
	if err := validateParams(env); err != nil {
		return err
	}
	return pub.PublishProto(ctx, tx, outbox.PublishProtoOptions{
		AggregateType: AggregateType,
		AggregateID:   env.GetMetadata().GetAggregateId(),
		Message:       env,
		Topic:         topicName,
		EventID:       env.GetMetadata().GetEventId(),
	})
}

func validate(env *notificationv1.NotificationEnvelope) error {
	if env == nil || env.GetMetadata() == nil {
		return errEmptyPayload()
	}
	m := env.GetMetadata()
	if m.GetEventId() == "" {
		return errEmptyEventID()
	}
	if m.GetAggregateId() == "" {
		return errEmptyAggregateID()
	}
	if m.GetOccurredAt() == nil || m.GetOccurredAt().GetSeconds() == 0 {
		return errEmptyOccurredAt()
	}
	// Locale is OPTIONAL. Every channel-owning consumer resolves the real
	// per-recipient locale (envelope hint -> user preference -> platform
	// default); a producer that pins a locale here overrides recipient
	// preference for every recipient — the exact bug the tenant-unification
	// pivot removed from order-service. Producers with genuine per-request
	// locale context may still set it as a hint.
	if m.GetType() == notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED {
		return errUnspecifiedType()
	}
	if len(m.GetChannels()) == 0 {
		return errEmptyChannels()
	}
	// Recipient addressing. An envelope is either person-addressed
	// (recipient_user_id) or tenant-addressed (recipient_tenant_id, resolved
	// to the live member set at delivery time by each channel-owning
	// consumer) — never both. Setting both is ambiguous and always rejected,
	// independent of channels. Channels that need a resolvable recipient
	// (IN_APP writes an inbox row; PUSH looks up push tokens) require exactly
	// one of the two; SMS/EMAIL-only directives carry their address in the
	// payload and may set neither (the preserved SMS special case).
	userID := m.GetRecipientUserId()
	tenantID := m.GetRecipientTenantId()
	if userID != "" && tenantID != "" {
		return errAmbiguousRecipient()
	}
	if userID == "" && tenantID == "" && requiresRecipient(m.GetChannels()) {
		return errEmptyRecipient()
	}
	if containsInApp(m.GetChannels()) {
		if m.GetDeepLink() == nil ||
			m.GetDeepLink().GetScreen() == notificationv1.DeepLinkScreen_DEEP_LINK_SCREEN_UNSPECIFIED {
			return errMissingDeepLink()
		}
	}
	if env.Payload == nil {
		return errEmptyPayload()
	}
	return nil
}

func validateParams(env *notificationv1.NotificationEnvelope) error {
	// Verbatim free-text types (PLATFORM_MESSAGE) carry their literal title/body
	// on the wire and have no catalog template / required-param contract. Skip
	// the catalog branch entirely; instead assert the text is non-empty so an
	// empty admin message never reaches the inbox.
	if notifyrender.IsVerbatim(env.GetMetadata().GetType()) {
		if pm := env.GetSendPlatformMessage(); pm.GetTitle() == "" && pm.GetBody() == "" {
			return errEmptyVerbatimText()
		}
		return nil
	}
	// Only validate required-param coverage when IN_APP is in channels;
	// EMAIL/SMS-only directives use their own producer-side fields and
	// notification-service tolerates absent ExtractParams (the existing
	// template renderer there has its own contract).
	if !containsInApp(env.GetMetadata().GetChannels()) {
		return nil
	}
	params, err := notifyrender.ExtractParams(env)
	if err != nil {
		return err
	}
	for _, required := range notifyrender.RequiredParams(env.GetMetadata().GetType()) {
		// Proto string getters return "" for unset scalar fields, so ExtractParams
		// always populates the key. Treat empty value as missing.
		if v, ok := params[required]; !ok || v == "" {
			return errMissingParam(env.GetMetadata().GetType(), required)
		}
	}
	return nil
}

// repairTypePrefix is the enum-name prefix every «Ремонт спецтехники» directive
// shares. The family is recognised by NAME rather than by a hand-written list of
// the 31 values because the list is the thing that would be forgotten: a
// thirty-second repair type minted in proto-contracts must be guarded the day it
// exists, not the day somebody remembers this file.
const repairTypePrefix = "NOTIFICATION_TYPE_REPAIR_"

// isRepairType reports whether t belongs to the repair family. The lookup is on
// NotificationType_name rather than t.String() so an unknown numeric value
// yields "" — which matches no prefix — instead of a decimal string.
func isRepairType(t notificationv1.NotificationType) bool {
	return strings.HasPrefix(notificationv1.NotificationType_name[int32(t)], repairTypePrefix)
}

// validateRepairChannels enforces spec §11 / D28: a repair directive takes
// EXACTLY the pair [IN_APP, PUSH] and no other channel.
//
// This is the guard, and until now there was none. The stated one — «repair has
// no rows in notification-service's email/SMS template table, so it cannot take
// those channels» — is not a guard at all: that table is consulted only when the
// dispatcher does not recognise a type, and every repair type is recognised by
// notifyrender. An envelope declaring EMAIL would have reached the email sender
// with the push title and body in it. What actually kept repair on the pair was
// that both producers hard-code it (order-service's repairChannels(),
// sale-service's per-service CHANNELS consts) — a convention, one edit from
// being untrue, with nothing red anywhere (critique 2026-09-23, NOTIF-09).
//
// It runs INSIDE the caller's transaction, beside the required-param check, and
// for the same reason: a rejected directive must roll the producer's own write
// back rather than be published as a malformed push.
//
// «Exactly» is read strictly — two entries, one IN_APP and one PUSH, in either
// order. A repeated channel is refused too: a duplicate is a producer bug, and
// every channel-owning consumer fans out per entry.
func validateRepairChannels(m *notificationv1.EnvelopeMetadata) error {
	if m == nil || !isRepairType(m.GetType()) {
		return nil
	}
	channels := m.GetChannels()
	if len(channels) != 2 {
		return errRepairChannels(m.GetType(), channels)
	}
	var inApp, push bool
	for _, c := range channels {
		switch c {
		case notificationv1.Channel_CHANNEL_IN_APP:
			inApp = true
		case notificationv1.Channel_CHANNEL_PUSH:
			push = true
		}
	}
	if !inApp || !push {
		return errRepairChannels(m.GetType(), channels)
	}
	return nil
}

func containsInApp(channels []notificationv1.Channel) bool {
	for _, c := range channels {
		if c == notificationv1.Channel_CHANNEL_IN_APP {
			return true
		}
	}
	return false
}

// requiresRecipient reports whether the channels list contains a channel
// that needs a resolvable recipient (exactly one of
// metadata.recipient_user_id / metadata.recipient_tenant_id). IN_APP needs
// it to write inbox rows; PUSH needs it to look up push tokens. SMS and
// EMAIL alone carry their address in the payload — no recipient required.
func requiresRecipient(channels []notificationv1.Channel) bool {
	for _, c := range channels {
		if c == notificationv1.Channel_CHANNEL_IN_APP ||
			c == notificationv1.Channel_CHANNEL_PUSH {
			return true
		}
	}
	return false
}
