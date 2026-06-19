// Package notifyrender renders notification templates for a typed
// NotificationEnvelope payload. Both notification-service and
// inbox-service depend on this package to render identically.
package notifyrender

import (
	"fmt"
	"net/http"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Reason codes — exported so callers and tests can assert on them.
// The Reason is the i18n key the frontend uses to look up localized prose,
// so it must remain stable; never rename without coordinating with the
// frontend's translation catalog.
const (
	// ReasonUnknownType signals a NotificationType not present in the catalog
	// (typeKey lookup miss). UNSPECIFIED and SYSTEM are reserved values that
	// trigger this reason.
	ReasonUnknownType = "NOTIFYRENDER_UNKNOWN_TYPE"

	// ReasonEmptyPayload signals an envelope whose oneof Payload is unset
	// (or an envelope pointer that is nil).
	ReasonEmptyPayload = "NOTIFYRENDER_EMPTY_PAYLOAD"

	// ReasonMissingParam signals a required template param that the caller
	// did not supply in the params map. The Params map on the AppError
	// carries the type name and the missing param name.
	ReasonMissingParam = "NOTIFYRENDER_MISSING_PARAM"

	// ReasonEmptyVerbatim signals a verbatim (free-text) directive whose
	// title AND body are both empty. Verbatim types (PLATFORM_MESSAGE) carry
	// their literal content on the wire; an empty pair has nothing to render.
	// This is defensive — the producer-side validator should reject it first.
	ReasonEmptyVerbatim = "NOTIFYRENDER_EMPTY_VERBATIM"
)

// ErrUnknownType returns the AppError for a NotificationType not in the catalog.
// Callers use errors.As to introspect Reason via *AppError.
func ErrUnknownType(t notificationv1.NotificationType) *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonUnknownType).
		Message(fmt.Sprintf("unknown notification type %s", t.String())).
		Params(map[string]any{"type": t.String()}).
		Build()
}

// ErrEmptyPayload returns the AppError for an envelope whose oneof payload
// is unset (or whose pointer is nil).
func ErrEmptyPayload() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyPayload).
		Message("envelope payload is empty").
		Build()
}

// ErrMissingParam returns the AppError for a required template param missing
// from the caller's params map.
func ErrMissingParam(t notificationv1.NotificationType, param string) *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonMissingParam).
		Message(fmt.Sprintf("missing required param %q for type %s", param, t.String())).
		Params(map[string]any{"type": t.String(), "param": param}).
		Build()
}

// ErrEmptyVerbatimText returns the AppError for a verbatim directive whose
// title and body are both empty.
func ErrEmptyVerbatimText() *commonerr.AppError {
	return commonerr.New(http.StatusInternalServerError).
		Reason(ReasonEmptyVerbatim).
		Message("verbatim directive has empty title and body").
		Build()
}
