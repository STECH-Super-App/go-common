package notifyrender

import (
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

func TestPartsPriceListRefusedExtractionAndRendering(t *testing.T) {
	typ := notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_REFUSED
	p := &notificationv1.SendPartsPriceListRefused{FileName: "price.xlsx", Reason: "invalid items"}
	env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: typ}, Payload: &notificationv1.NotificationEnvelope_SendPartsPriceListRefused{SendPartsPriceListRefused: p}}
	r := NewRenderer(i18n.NewOverlayBundle(BaselineEN, ""))
	for _, name := range []string{"", "Example Shop"} {
		p.ShopDisplayName = name
		params, err := ExtractParams(env)
		if err != nil {
			t.Fatal(err)
		}
		if params["file_name"] != p.FileName || params["reason"] != p.Reason || params["shop_display_name"] != name {
			t.Fatalf("params=%v", params)
		}
		title, body, err := r.Render(typ, params, "en")
		if err != nil {
			t.Fatal(err)
		}
		if title == "" || !strings.Contains(body, p.FileName) || !strings.Contains(body, p.Reason) || (name != "" && !strings.Contains(body, name)) || (name == "" && strings.Contains(body, "''")) {
			t.Fatalf("title=%q body=%q", title, body)
		}
	}
	for _, required := range []string{"file_name", "reason"} {
		found := false
		for _, p := range RequiredParams(typ) {
			if p == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s must be required", required)
		}
	}
}
