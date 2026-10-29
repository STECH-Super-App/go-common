package notifyoutbox

import (
	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"testing"
)

func TestPartsPriceListRefusedValidation(t *testing.T) {
	e := validEnvelope(t)
	e.Metadata.Type = notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_REFUSED
	e.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_IN_APP, notificationv1.Channel_CHANNEL_PUSH}
	p := &notificationv1.SendPartsPriceListRefused{FileName: "price.xlsx", Reason: "invalid data"}
	e.Payload = &notificationv1.NotificationEnvelope_SendPartsPriceListRefused{SendPartsPriceListRefused: p}
	if err := validatePartsPriceListRefusedChannels(e.Metadata); err != nil {
		t.Fatal(err)
	}
	if err := validateParams(e); err != nil {
		t.Fatal(err)
	}
	p.Reason = ""
	assertReason(t, validateParams(e), ReasonMissingParam)
	p.Reason = "invalid data"
	p.FileName = ""
	assertReason(t, validateParams(e), ReasonMissingParam)
	p.FileName = "price.xlsx"
	for _, channels := range [][]notificationv1.Channel{{notificationv1.Channel_CHANNEL_IN_APP}, {notificationv1.Channel_CHANNEL_PUSH}, {notificationv1.Channel_CHANNEL_IN_APP, notificationv1.Channel_CHANNEL_EMAIL}, {notificationv1.Channel_CHANNEL_IN_APP, notificationv1.Channel_CHANNEL_IN_APP}} {
		e.Metadata.Channels = channels
		assertReason(t, validatePartsPriceListRefusedChannels(e.Metadata), ReasonPartsPriceListRefusedChannels)
	}
	e.Metadata.Channels = []notificationv1.Channel{notificationv1.Channel_CHANNEL_PUSH, notificationv1.Channel_CHANNEL_IN_APP}
	if err := validatePartsPriceListRefusedChannels(e.Metadata); err != nil {
		t.Fatal(err)
	}
}
