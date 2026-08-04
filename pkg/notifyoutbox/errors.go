// Package notifyoutbox provides a typed wrapper around
// outbox.Publisher.PublishProto for TOPIC_NOTIFICATION_EVENTS.
// Validates the envelope before writing the outbox row.
package notifyoutbox

import (
	"fmt"
	"net/http"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Reason codes — exported for callers, tests, and frontend i18n lookups.
// Constructors are package-private (errXxx) because PublishDirective is
// the only function that returns them; downstream code asserts via
// errors.As(err, &*AppError) and inspects Reason.
const (
	ReasonEmptyEventID       = "NOTIFYOUTBOX_EMPTY_EVENT_ID"
	ReasonEmptyAggregateID   = "NOTIFYOUTBOX_EMPTY_AGGREGATE_ID"
	ReasonEmptyOccurredAt    = "NOTIFYOUTBOX_EMPTY_OCCURRED_AT"
	ReasonEmptyRecipient     = "NOTIFYOUTBOX_EMPTY_RECIPIENT"
	ReasonAmbiguousRecipient = "NOTIFYOUTBOX_AMBIGUOUS_RECIPIENT"
	ReasonUnspecifiedType    = "NOTIFYOUTBOX_UNSPECIFIED_TYPE"
	ReasonEmptyChannels      = "NOTIFYOUTBOX_EMPTY_CHANNELS"
	ReasonMissingDeepLink    = "NOTIFYOUTBOX_MISSING_DEEP_LINK"
	ReasonEmptyPayload       = "NOTIFYOUTBOX_EMPTY_PAYLOAD"
	ReasonMissingParam       = "NOTIFYOUTBOX_MISSING_PARAM"
	ReasonNilPublisher       = "NOTIFYOUTBOX_NIL_PUBLISHER"
	ReasonEmptyVerbatim      = "NOTIFYOUTBOX_EMPTY_VERBATIM"
)

func errEmptyEventID() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyEventID).
		Message("metadata.event_id is empty").
		Build()
}

func errEmptyAggregateID() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyAggregateID).
		Message("metadata.aggregate_id is empty").
		Build()
}

func errEmptyOccurredAt() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyOccurredAt).
		Message("metadata.occurred_at is zero").
		Build()
}

func errEmptyRecipient() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyRecipient).
		Message("neither metadata.recipient_user_id nor metadata.recipient_tenant_id is set (channels contains IN_APP or PUSH — set exactly one)").
		Build()
}

func errAmbiguousRecipient() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonAmbiguousRecipient).
		Message("metadata sets both recipient_user_id and recipient_tenant_id (ambiguous addressing — set exactly one)").
		Build()
}

func errUnspecifiedType() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonUnspecifiedType).
		Message("metadata.type is UNSPECIFIED").
		Build()
}

func errEmptyChannels() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyChannels).
		Message("metadata.channels is empty").
		Build()
}

func errMissingDeepLink() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonMissingDeepLink).
		Message("IN_APP channel requires metadata.deep_link.screen").
		Build()
}

func errEmptyPayload() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyPayload).
		Message("envelope payload is unset").
		Build()
}

func errMissingParam(t notificationv1.NotificationType, param string) *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonMissingParam).
		Message(fmt.Sprintf("missing required param %q for type %s", param, t.String())).
		Params(map[string]any{"type": t.String(), "param": param}).
		Build()
}

func errNilPublisher() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonNilPublisher).
		Message("publisher is nil").
		Build()
}

func errEmptyVerbatimText() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyVerbatim).
		Message("verbatim directive (PLATFORM_MESSAGE) has empty title and body").
		Build()
}
