package notifyoutbox

import (
	"net/http"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Type 175 has exactly the IN_APP and PUSH pair; unlike type 70, no email.
func validatePartsPriceListRefusedChannels(m *notificationv1.EnvelopeMetadata) error {
	if m == nil || m.GetType() != notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_REFUSED {
		return nil
	}
	channels := m.GetChannels()
	if len(channels) == 2 && ((channels[0] == notificationv1.Channel_CHANNEL_IN_APP && channels[1] == notificationv1.Channel_CHANNEL_PUSH) || (channels[1] == notificationv1.Channel_CHANNEL_IN_APP && channels[0] == notificationv1.Channel_CHANNEL_PUSH)) {
		return nil
	}
	return commonerr.New(http.StatusInternalServerError).Reason(ReasonPartsPriceListRefusedChannels).Message("Price list refusal requires exactly IN_APP and PUSH").Build()
}
