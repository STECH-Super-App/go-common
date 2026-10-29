package notifyoutbox

import (
	"net/http"
	"strings"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

func errMessengerDirective() error {
	return commonerr.New(http.StatusInternalServerError).Reason(ReasonMessengerDirective).Message("Invalid messenger notification directive").Build()
}

func messengerNotice(t notificationv1.NotificationType) bool {
	return t == notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED || t == notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_OPENED || t == notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_CLOSED
}

func validMessengerID(s string) bool {
	id, e := input.UUID(s)
	return e == nil && id != "00000000-0000-0000-0000-000000000000"
}

// New actor-scoped notices are always person-addressed and IN_APP only. This
// intentionally does not change channel semantics of existing CHAT_MESSAGE.
func validateMessengerMetadata(m *notificationv1.EnvelopeMetadata) error {
	if m == nil || !messengerNotice(m.GetType()) {
		return nil
	}
	if !validMessengerID(m.GetRecipientUserId()) || !validMessengerID(m.GetActingTenantId()) || m.GetRecipientTenantId() != "" || len(m.GetChannels()) != 1 || m.GetChannels()[0] != notificationv1.Channel_CHANNEL_IN_APP {
		return errMessengerDirective()
	}
	return nil
}

const maxMessengerObjectStatusRunes = 64

// validateMessengerObjectRef checks the object reference fields shared by the
// section/appeal notices: a required known service, an optional bounded one-line
// status code and an optional cover file id. The cover is an id, never a URL.
func validateMessengerObjectRef(service, status, cover string) error {
	switch service {
	case "sale", "rent", "delivery", "repair", "parts":
	default:
		return errMessengerDirective()
	}
	if status != "" {
		if _, f := input.Line(status, maxMessengerObjectStatusRunes); f != nil {
			return errMessengerDirective()
		}
	}
	if cover != "" {
		if _, f := input.UUID(cover); f != nil {
			return errMessengerDirective()
		}
	}
	return nil
}

func validateMessengerDirective(env *notificationv1.NotificationEnvelope) error {
	m := env.GetMetadata()
	if err := validateMessengerMetadata(m); err != nil {
		return err
	}
	if p := env.GetSendChatMessageReceived(); p != nil && p.GetSectionId() != "" {
		if err := validateMessengerPush(m, p); err != nil {
			return err
		}
	}
	if !messengerNotice(m.GetType()) {
		return nil
	}
	return validateMessengerNotice(env, m)
}

// validateMessengerPush validates a section-scoped CHAT_MESSAGE push.
func validateMessengerPush(m *notificationv1.EnvelopeMetadata, p *notificationv1.SendChatMessageReceived) error {
	if m.GetType() != notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE || len(m.GetChannels()) != 1 || m.GetChannels()[0] != notificationv1.Channel_CHANNEL_PUSH || !validMessengerID(p.GetSectionId()) || !validMessengerID(p.GetMessageId()) || strings.TrimSpace(p.GetSenderDisplayName()) == "" {
		return errMessengerDirective()
	}
	if err := validateMessengerPushScope(m, p); err != nil {
		return err
	}
	switch p.GetPreviewKind() {
	case "", "TEXT":
		if strings.TrimSpace(p.GetPreview()) == "" {
			return errMessengerDirective()
		}
	case "PHOTO", "DOCUMENT", "LOCATION", "ATTACHMENTS":
		if strings.TrimSpace(p.GetPreview()) != "" {
			return errMessengerDirective()
		}
	default:
		return errMessengerDirective()
	}
	return nil
}

func validateMessengerPushScope(m *notificationv1.EnvelopeMetadata, p *notificationv1.SendChatMessageReceived) error {
	actor, sender, counterpart := m.GetActingTenantId(), p.GetSenderTenantId(), p.GetCounterpartTenantId()
	if actor == "" && sender == "" && counterpart == "" {
		// Support is person-global, and has no business-object requirement.
		if !validMessengerID(m.GetRecipientUserId()) || m.GetRecipientTenantId() != "" {
			return errMessengerDirective()
		}
		return nil
	}
	// The counterpart is the receiving actor's opposite, i.e. the sender.
	if !validMessengerID(actor) || !validMessengerID(sender) || !validMessengerID(counterpart) || !strings.EqualFold(sender, counterpart) || strings.EqualFold(actor, sender) || !validMessengerID(p.GetObjectId()) || strings.TrimSpace(p.GetObjectType()) == "" || strings.TrimSpace(p.GetObjectTitle()) == "" {
		return errMessengerDirective()
	}
	if m.GetRecipientTenantId() != "" && !strings.EqualFold(m.GetRecipientTenantId(), actor) {
		return errMessengerDirective()
	}
	return nil
}

// messengerNoticeFields is the common shape of the three section/appeal notices.
type messengerNoticeFields struct {
	section, objectID, objectType, title, kind, counterpart string
	service, status, cover                                  string
}

func validateMessengerNotice(env *notificationv1.NotificationEnvelope, m *notificationv1.EnvelopeMetadata) error {
	var f messengerNoticeFields
	closed := m.GetType() == notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED
	switch m.GetType() {
	case notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED:
		p := env.GetSendChatSectionClosed()
		if p == nil {
			return errMessengerDirective()
		}
		f = messengerNoticeFields{section: p.GetSectionId(), objectID: p.GetObjectId(), objectType: p.GetObjectType(), title: p.GetObjectTitle(), counterpart: p.GetCounterpartTenantId(), service: p.GetObjectService(), status: p.GetObjectStatus(), cover: p.GetObjectCoverFileId()}
		if !validMessengerID(f.counterpart) || strings.EqualFold(f.counterpart, m.GetActingTenantId()) {
			return errMessengerDirective()
		}
	case notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_OPENED:
		p := env.GetSendChatAppealOpened()
		if p == nil {
			return errMessengerDirective()
		}
		f = messengerNoticeFields{section: p.GetSectionId(), objectID: p.GetObjectId(), objectType: p.GetObjectType(), title: p.GetObjectTitle(), kind: p.GetAppealKind(), service: p.GetObjectService(), status: p.GetObjectStatus(), cover: p.GetObjectCoverFileId()}
	case notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_CLOSED:
		p := env.GetSendChatAppealClosed()
		if p == nil {
			return errMessengerDirective()
		}
		f = messengerNoticeFields{section: p.GetSectionId(), objectID: p.GetObjectId(), objectType: p.GetObjectType(), title: p.GetObjectTitle(), kind: p.GetAppealKind(), service: p.GetObjectService(), status: p.GetObjectStatus(), cover: p.GetObjectCoverFileId()}
	}
	if !validMessengerID(f.section) || !validMessengerID(f.objectID) || strings.TrimSpace(f.objectType) == "" || strings.TrimSpace(f.title) == "" || (!closed && strings.TrimSpace(f.kind) == "") {
		return errMessengerDirective()
	}
	return validateMessengerObjectRef(f.service, f.status, f.cover)
}
