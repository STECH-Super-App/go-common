package notifyoutbox

import (
	"context"

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
	if m.GetLocale() == "" {
		return errEmptyLocale()
	}
	if m.GetType() == notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED {
		return errUnspecifiedType()
	}
	if len(m.GetChannels()) == 0 {
		return errEmptyChannels()
	}
	if m.GetRecipientUserId() == "" && requiresRecipient(m.GetChannels()) {
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

func containsInApp(channels []notificationv1.Channel) bool {
	for _, c := range channels {
		if c == notificationv1.Channel_CHANNEL_IN_APP {
			return true
		}
	}
	return false
}

// requiresRecipient reports whether the channels list contains a channel
// that needs metadata.recipient_user_id. IN_APP needs it to write the
// inbox row; PUSH needs it to look up the user's registered push tokens.
// SMS and EMAIL alone carry their address in the payload — no recipient
// required.
func requiresRecipient(channels []notificationv1.Channel) bool {
	for _, c := range channels {
		if c == notificationv1.Channel_CHANNEL_IN_APP ||
			c == notificationv1.Channel_CHANNEL_PUSH {
			return true
		}
	}
	return false
}
