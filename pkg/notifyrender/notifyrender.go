package notifyrender

import (
	"fmt"
	"net/http"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// ReasonRenderResolveFailed is emitted when the underlying i18n.Bundle
// fails to resolve a template key (missing key in the target locale's
// TOML, malformed template, etc.). Distinct from ReasonUnknownType
// (catalog miss) and ReasonMissingParam (caller forgot a param).
const ReasonRenderResolveFailed = "NOTIFYRENDER_RESOLVE_FAILED"

// Render returns the rendered title and body for the given notification
// type, params, and locale. Returns the typed AppError for any failure
// mode (ErrUnknownType, ErrMissingParam, or a resolve failure).
//
// The render is pure: same inputs always return same outputs. Consumers
// that store rendered text (e.g. inbox-service) call Render once at
// insert time and lock the result in the row.
func Render(
	t notificationv1.NotificationType,
	params map[string]string,
	locale string,
) (title string, body string, err error) {
	key, ok := typeKey[t]
	if !ok {
		return "", "", ErrUnknownType(t)
	}
	for _, required := range requiredParams[t] {
		if _, present := params[required]; !present {
			return "", "", ErrMissingParam(t, required)
		}
	}
	titleKey := key + ".title"
	bodyKey := key + ".body"

	asAny := toAny(params)

	title, err = bundle.Resolve(locale, titleKey, asAny)
	if err != nil {
		return "", "", commonerr.New(http.StatusInternalServerError).
			Reason(ReasonRenderResolveFailed).
			Message(fmt.Sprintf("resolve title %s: %v", titleKey, err)).
			Params(map[string]any{"key": titleKey, "locale": locale}).
			Cause(err).
			Build()
	}
	body, err = bundle.Resolve(locale, bodyKey, asAny)
	if err != nil {
		return "", "", commonerr.New(http.StatusInternalServerError).
			Reason(ReasonRenderResolveFailed).
			Message(fmt.Sprintf("resolve body %s: %v", bodyKey, err)).
			Params(map[string]any{"key": bodyKey, "locale": locale}).
			Cause(err).
			Build()
	}
	return title, body, nil
}

func toAny(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
