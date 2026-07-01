package notifyrender

import (
	"fmt"
	"net/http"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

// ReasonRenderResolveFailed is emitted when the underlying i18n.Bundle
// fails to resolve a template key (missing key in the target locale,
// malformed template, etc.). Distinct from ReasonUnknownType
// (catalog miss) and ReasonMissingParam (caller forgot a param).
const ReasonRenderResolveFailed = "NOTIFYRENDER_RESOLVE_FAILED"

// Renderer resolves notification templates against an i18n bundle supplied
// at construction time. The strings are NOT compiled in; the consuming
// service builds the bundle from a mounted source at startup and injects it.
type Renderer struct {
	bundle *i18n.Bundle
}

// NewRenderer returns a Renderer backed by the given bundle.
func NewRenderer(bundle *i18n.Bundle) *Renderer {
	return &Renderer{bundle: bundle}
}

// Render returns the rendered title and body for the given notification
// type, params, and locale. The render is pure: same inputs, same outputs.
// Returns the typed AppError for any failure mode (ErrUnknownType,
// ErrMissingParam, or a resolve failure).
//
// Consumers that store rendered text (e.g. inbox-service) call Render once
// at insert time and lock the result in the row.
func (r *Renderer) Render(
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

	title, err = r.bundle.Resolve(locale, titleKey, asAny)
	if err != nil {
		return "", "", commonerr.New(http.StatusInternalServerError).
			Reason(ReasonRenderResolveFailed).
			Message(fmt.Sprintf("resolve title %s: %v", titleKey, err)).
			Params(map[string]any{"key": titleKey, "locale": locale}).
			Cause(err).
			Build()
	}
	body, err = r.bundle.Resolve(locale, bodyKey, asAny)
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

// IsVerbatim reports whether type t carries its own literal title/body in the
// payload and must skip the catalog template lookup. Verbatim types have no
// typeKey/requiredParams entry; consumers branch on this before calling Render
// and use RenderVerbatim instead.
//
// Today only NOTIFICATION_TYPE_PLATFORM_MESSAGE (admin free-text) is verbatim.
func IsVerbatim(t notificationv1.NotificationType) bool {
	return t == notificationv1.NotificationType_NOTIFICATION_TYPE_PLATFORM_MESSAGE
}

// RenderVerbatim returns the literal title/body for a verbatim type, pulled
// straight from the params map produced by ExtractParams. There is no template
// and no locale: the text on the wire is already the recipient-facing content.
//
// Returns ErrEmptyVerbatimText when both title and body are empty (a producer
// bug — the notifyoutbox validator rejects this at publish time, so this is a
// defensive guard for the consumer render path).
func RenderVerbatim(params map[string]string) (title string, body string, err error) {
	title = params["title"]
	body = params["body"]
	if title == "" && body == "" {
		return "", "", ErrEmptyVerbatimText()
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
