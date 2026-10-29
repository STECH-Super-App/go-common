package notifyoutbox

import (
	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"strings"
	"testing"
)

func TestMessengerNoticeChannelAndActor(t *testing.T) {
	types := []notificationv1.NotificationType{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED, notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_OPENED, notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_CLOSED}
	for _, typ := range types {
		t.Run(typ.String(), func(t *testing.T) {
			m := &notificationv1.EnvelopeMetadata{Type: typ, RecipientUserId: "12345678-1234-1234-1234-123456789abc", ActingTenantId: "23456789-1234-1234-1234-123456789abc", Channels: []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP}}
			if err := validateMessengerMetadata(m); err != nil {
				t.Fatal(err)
			}
			m.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_PUSH}
			if err := validateMessengerMetadata(m); err == nil {
				t.Fatal("push accepted")
			}
			m.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP}
			m.ActingTenantId = ""
			if err := validateMessengerMetadata(m); err == nil {
				t.Fatal("missing actor accepted")
			}
			m.ActingTenantId = "bad"
			if err := validateMessengerMetadata(m); err == nil {
				t.Fatal("invalid actor accepted")
			}
		})
	}
}

func TestMessengerNoticePayloadValidation(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789abc"
	cases := []struct {
		typ     notificationv1.NotificationType
		payload any
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED, &notificationv1.SendChatSectionClosed{SectionId: id, CounterpartTenantId: "34567890-1234-1234-1234-123456789abc", ObjectType: "RENT", ObjectId: id, ObjectTitle: "Excavator", ObjectService: "rent"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_OPENED, &notificationv1.SendChatAppealOpened{SectionId: id, ObjectType: "RENT", ObjectId: id, ObjectTitle: "Excavator", ObjectService: "rent", AppealKind: "PROBLEM"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_CLOSED, &notificationv1.SendChatAppealClosed{SectionId: id, ObjectType: "RENT", ObjectId: id, ObjectTitle: "Excavator", ObjectService: "rent", AppealKind: "PROBLEM"}},
	}
	for _, tt := range cases {
		t.Run(tt.typ.String(), func(t *testing.T) {
			env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: tt.typ, RecipientUserId: id, ActingTenantId: "23456789-1234-1234-1234-123456789abc", Channels: []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP}}}
			switch p := tt.payload.(type) {
			case *notificationv1.SendChatSectionClosed:
				env.Payload = &notificationv1.NotificationEnvelope_SendChatSectionClosed{SendChatSectionClosed: p}
			case *notificationv1.SendChatAppealOpened:
				env.Payload = &notificationv1.NotificationEnvelope_SendChatAppealOpened{SendChatAppealOpened: p}
			case *notificationv1.SendChatAppealClosed:
				env.Payload = &notificationv1.NotificationEnvelope_SendChatAppealClosed{SendChatAppealClosed: p}
			}
			if err := validateMessengerDirective(env); err != nil {
				t.Fatal(err)
			}
			if p, ok := tt.payload.(*notificationv1.SendChatSectionClosed); ok {
				original := p.CounterpartTenantId
				p.CounterpartTenantId = env.Metadata.ActingTenantId
				if err := validateMessengerDirective(env); err == nil {
					t.Fatal("same actor and counterpart accepted")
				}
				p.CounterpartTenantId = original
			}
			setRef := func(service, status, cover string) {
				switch p := tt.payload.(type) {
				case *notificationv1.SendChatSectionClosed:
					p.ObjectService, p.ObjectStatus, p.ObjectCoverFileId = service, status, cover
				case *notificationv1.SendChatAppealOpened:
					p.ObjectService, p.ObjectStatus, p.ObjectCoverFileId = service, status, cover
				case *notificationv1.SendChatAppealClosed:
					p.ObjectService, p.ObjectStatus, p.ObjectCoverFileId = service, status, cover
				}
			}
			for _, ref := range []struct {
				name, service, status, cover string
				ok                           bool
			}{
				{"all set", "parts", "ACTIVE", id, true},
				{"optionals empty", "sale", "", "", true},
				{"missing service", "", "", "", false},
				{"unknown service", "orders", "", "", false},
				{"service case", "Rent", "", "", false},
				{"multiline status", "rent", "a\nb", "", false},
				{"long status", "rent", strings.Repeat("x", 65), "", false},
				{"malformed cover", "rent", "", "not-a-uuid", false},
				{"cover url", "rent", "", "https://host/x.jpg", false},
			} {
				setRef(ref.service, ref.status, ref.cover)
				if err := validateMessengerDirective(env); (err == nil) != ref.ok {
					t.Fatalf("%s: err=%v", ref.name, err)
				}
			}
			setRef("rent", "", "")
			env.Payload = nil
			if err := validateMessengerDirective(env); err == nil {
				t.Fatal("missing payload accepted")
			}
		})
	}
}

func TestNewChatPushValidation(t *testing.T) {
	p := &notificationv1.SendChatMessageReceived{SectionId: "12345678-1234-1234-1234-123456789abc", MessageId: "23456789-1234-1234-1234-123456789abc", ObjectId: "34567890-1234-1234-1234-123456789abc", ObjectType: "RENT", ObjectTitle: "Excavator", SenderDisplayName: "Alice", PreviewKind: "PHOTO", SenderTenantId: "45678901-1234-1234-1234-123456789abc", CounterpartTenantId: "45678901-1234-1234-1234-123456789abc"}
	env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, RecipientUserId: "12345678-1234-1234-1234-123456789abc", ActingTenantId: "56789012-1234-1234-1234-123456789abc", Channels: []notificationv1.Channel{notificationv1.Channel_CHANNEL_PUSH}}, Payload: &notificationv1.NotificationEnvelope_SendChatMessageReceived{SendChatMessageReceived: p}}
	if err := validateMessengerDirective(env); err != nil {
		t.Fatal(err)
	}
	p.CounterpartTenantId = env.Metadata.ActingTenantId
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("counterpart mismatch accepted")
	}
	p.CounterpartTenantId = p.SenderTenantId
	env.Metadata.ActingTenantId = p.SenderTenantId
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("same receiving and sending actor accepted")
	}
	env.Metadata.ActingTenantId = "56789012-1234-1234-1234-123456789abc"
	p.SenderTenantId = ""
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("partial business identity accepted")
	}
	p.SenderTenantId = p.CounterpartTenantId
	p.PreviewKind = "FUTURE"
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("unknown preview accepted")
	}
	p.PreviewKind = "PHOTO"
	env.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP}
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("in-app accepted")
	}
}

func TestSupportChatPushHasPersonScope(t *testing.T) {
	p := &notificationv1.SendChatMessageReceived{SectionId: "12345678-1234-1234-1234-123456789abc", MessageId: "23456789-1234-1234-1234-123456789abc", SenderDisplayName: "Support", PreviewKind: "TEXT", Preview: "Hello"}
	env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, RecipientUserId: "34567890-1234-1234-1234-123456789abc", Channels: []notificationv1.Channel{notificationv1.Channel_CHANNEL_PUSH}}, Payload: &notificationv1.NotificationEnvelope_SendChatMessageReceived{SendChatMessageReceived: p}}
	if err := validateMessengerDirective(env); err != nil {
		t.Fatal(err)
	}
	p.CounterpartTenantId = "45678901-1234-1234-1234-123456789abc"
	if err := validateMessengerDirective(env); err == nil {
		t.Fatal("partial support scope accepted")
	}
}

func TestLegacyChatChannelRemainsAllowed(t *testing.T) {
	m := &notificationv1.EnvelopeMetadata{Type: notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, Channels: []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP, notificationv1.Channel_CHANNEL_PUSH}}
	if err := validateMessengerMetadata(m); err != nil {
		t.Fatal(err)
	}
}
