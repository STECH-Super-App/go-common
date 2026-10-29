package notifyrender

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

func TestMessengerRendering(t *testing.T) {
	r := NewRenderer(i18n.NewOverlayBundle(BaselineEN, ""))
	cases := []struct {
		typ    notificationv1.NotificationType
		params map[string]string
		want   string
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_SECTION_CLOSED, map[string]string{"object_title": "Excavator", "object_number": "42"}, "Excavator"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_OPENED, map[string]string{"object_title": "Excavator", "object_number": "42", "appeal_kind": "problem"}, "Excavator"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_APPEAL_CLOSED, map[string]string{"object_title": "Excavator", "object_number": "42", "appeal_kind": "problem"}, "Excavator"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "Alice", "preview": "hello", "object_title": "Excavator"}, "Excavator"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "Alice", "preview": "", "preview_kind": "PHOTO"}, "Photo"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "Alice", "preview": "", "preview_kind": "DOCUMENT"}, "Document"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "Alice", "preview": "", "preview_kind": "LOCATION"}, "Location"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "Alice", "preview": "", "preview_kind": "ATTACHMENTS"}, "Attachments"},
	}
	for _, tt := range cases {
		t.Run(tt.typ.String()+tt.want, func(t *testing.T) {
			title, body, err := r.Render(tt.typ, tt.params, "en")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(title+body, tt.want) {
				t.Fatalf("got %q %q", title, body)
			}
		})
	}
}

func TestMessengerPreviewUsesRecipientLocale(t *testing.T) {
	dir := t.TempDir()
	content := `{"chat_message":{"title":"{{.sender_name}}","body":"{{if eq .preview_kind \"PHOTO\"}}localized-photo{{else}}{{.preview}}{{end}}"}}`
	if err := os.WriteFile(filepath.Join(dir, "ru.json"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := i18n.NewOverlayBundle(BaselineEN, dir, i18n.WithAllowedParams(AllowedParamsByKey))
	bundle.Load(context.Background())
	_, body, err := NewRenderer(bundle).Render(notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE, map[string]string{"sender_name": "A", "preview": "", "preview_kind": "PHOTO"}, "ru")
	if err != nil || body != "localized-photo" {
		t.Fatalf("body %q err %v", body, err)
	}
}

func TestMessengerPayloadExtraction(t *testing.T) {
	cases := []struct {
		env  *notificationv1.NotificationEnvelope
		want string
	}{
		{&notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendChatSectionClosed{SendChatSectionClosed: &notificationv1.SendChatSectionClosed{ObjectTitle: "closed"}}}, "closed"},
		{&notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendChatAppealOpened{SendChatAppealOpened: &notificationv1.SendChatAppealOpened{ObjectTitle: "opened"}}}, "opened"},
		{&notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendChatAppealClosed{SendChatAppealClosed: &notificationv1.SendChatAppealClosed{ObjectTitle: "closed appeal"}}}, "closed appeal"},
		{&notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendChatMessageReceived{SendChatMessageReceived: &notificationv1.SendChatMessageReceived{ObjectTitle: "message", SenderDisplayName: "Alice"}}}, "message"},
	}
	for _, tt := range cases {
		params, err := ExtractParams(tt.env)
		if err != nil || params["object_title"] != tt.want {
			t.Fatalf("params=%v err=%v", params, err)
		}
	}
}
