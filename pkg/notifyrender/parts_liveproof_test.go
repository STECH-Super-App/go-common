package notifyrender

import (
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// partsLiveCase is one directive as it appears on the wire: the type its
// metadata carries and the payload arm its producer sets.
type partsLiveCase struct {
	nt   notificationv1.NotificationType
	name string
	env  *notificationv1.NotificationEnvelope
}

// partsLiveEnvelopes mirrors, field for field, the directives
// sale-service's PartsNotificationDirectiveBuilder actually writes into
// parts_storefront.outbox_messages. The first ten are the ones with a PHP
// producer today; the last two are the price-file importer's pair, which has no
// producer yet.
//
// Values are taken from a real notification.events.dlq.inbox dump on the local
// compose stack (2026-08-31), not invented — that is what makes this a
// reproduction rather than a mock.
func partsLiveEnvelopes() []partsLiveCase {
	cases := partsLiveCases()
	// The metadata type is stamped here rather than repeated in every literal:
	// ExtractParams reads it to NAME the type in ErrUnknownType, so without it the
	// failure would read NOTIFICATION_TYPE_UNSPECIFIED and stop matching the line
	// inbox-service actually writes to the DLQ.
	for i := range cases {
		cases[i].env.Metadata = &notificationv1.EnvelopeMetadata{Type: cases[i].nt}
	}
	return cases
}

func partsLiveCases() []partsLiveCase {
	return []partsLiveCase{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_HIDDEN_BY_ADMIN, "OFFER_HIDDEN_BY_ADMIN", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsOfferHiddenByAdmin{
				SendPartsOfferHiddenByAdmin: &notificationv1.SendPartsOfferHiddenByAdmin{
					ProductName: "Тяга радиатора ДВС L=320",
					Reason:      "Артикул не соответствует наименованию",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_SANCTION_LIFTED, "OFFER_SANCTION_LIFTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsOfferSanctionLifted{
				SendPartsOfferSanctionLifted: &notificationv1.SendPartsOfferSanctionLifted{
					ProductName: "Тяга радиатора ДВС L=320",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED, "SHOP_SANCTION_LIFTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsShopSanctionLifted{
				SendPartsShopSanctionLifted: &notificationv1.SendPartsShopSanctionLifted{
					RemainingCauses: nil,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED, "SHOP_VERIFICATION_RESTORED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsShopVerificationRestored{
				SendPartsShopVerificationRestored: &notificationv1.SendPartsShopVerificationRestored{
					RemainingCauses: []string{"PRICE_STALE"},
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK, "OFFER_BACK_IN_STOCK", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsOfferBackInStock{
				SendPartsOfferBackInStock: &notificationv1.SendPartsOfferBackInStock{
					ProductName: "Тяга радиатора ДВС L=320",
					Price:       "4 890",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED, "FAVORITE_PRICE_DROPPED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsFavoritePriceDropped{
				SendPartsFavoritePriceDropped: &notificationv1.SendPartsFavoritePriceDropped{
					ProductName: "Тяга радиатора ДВС L=320",
					OldPrice:    "5 200",
					NewPrice:    "4 890",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_OFFER_APPEARED, "SUBSCRIPTION_OFFER_APPEARED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsSubscriptionOfferAppeared{
				SendPartsSubscriptionOfferAppeared: &notificationv1.SendPartsSubscriptionOfferAppeared{
					ProductName:    "Датчик нейтральной передачи",
					SubscriptionId: "01920060-0001-7000-8000-000000600001",
					PriceFrom:      "1 250",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_EXPIRING, "SUBSCRIPTION_EXPIRING", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsSubscriptionExpiring{
				SendPartsSubscriptionExpiring: &notificationv1.SendPartsSubscriptionExpiring{
					ProductName:    "Датчик нейтральной передачи",
					SubscriptionId: "01920060-0002-7000-8000-000000600002",
					DaysLeft:       3,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_STALE_WARNING, "PRICE_LIST_STALE_WARNING", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsPriceListStaleWarning{
				SendPartsPriceListStaleWarning: &notificationv1.SendPartsPriceListStaleWarning{
					DaysSinceUpload: 13,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFERS_HIDDEN_PRICE_LIST_STALE, "OFFERS_HIDDEN_PRICE_LIST_STALE", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsOffersHiddenPriceListStale{
				SendPartsOffersHiddenPriceListStale: &notificationv1.SendPartsOffersHiddenPriceListStale{
					DaysSinceUpload: 14,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED, "PRICE_LIST_PROCESSED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsPriceListProcessed{
				SendPartsPriceListProcessed: &notificationv1.SendPartsPriceListProcessed{
					PublishedCount: 120, MatchingCount: 7, ErrorCount: 0, NewAddressCount: 4,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_FILE_FAILED, "PRICE_LIST_FILE_FAILED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsPriceListFileFailed{
				SendPartsPriceListFileFailed: &notificationv1.SendPartsPriceListFileFailed{
					FileName: "price-08-2026.xlsx",
				}}}},
	}
}

// TestPartsDirectivesRenderRatherThanDeadLetter is the executable form of the
// question «does a parts directive render, or does it dead-letter?».
//
// It is a REPRODUCTION of the live failure, not a unit test of a map: the twelve
// envelopes below are the payloads sale-service actually emits, and the failure
// this test catches is byte-identical to the one inbox-service writes onto
// notification.events.dlq.inbox — `extract params: unknown notification type …`
// — because inbox-service calls this exact ExtractParams and wraps its error raw
// (internal/application/ingestion/service.go). A parts type with no case here is
// a dead letter there, on the first delivery, with no retry tier in between.
//
// Run with -v to see the rendered title and body for every type.
func TestPartsDirectivesRenderRatherThanDeadLetter(t *testing.T) {
	r := testRendererFull(t)

	for _, tc := range partsLiveEnvelopes() {
		t.Run(tc.name, func(t *testing.T) {
			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("DEAD LETTER — ExtractParams: %v", err)
			}
			for _, loc := range []string{"en", "ru"} {
				title, body, err := r.Render(tc.nt, params, loc)
				if err != nil {
					t.Fatalf("DEAD LETTER — Render(%s): %v", loc, err)
				}
				if title == "" || body == "" {
					t.Fatalf("%s: empty title/body (title=%q body=%q)", loc, title, body)
				}
				t.Logf("%s\n    title: %s\n    body:  %s", loc, title, body)
			}
		})
	}
}

// TestPartsProducedTypesAreAllInTheCatalog is the half the fixture list above
// cannot cover: it walks the payload arms one at a time and would still pass if
// somebody deleted a case, because a missing arm is simply a missing fixture.
// This one asserts the SET — every parts type the PHP builder can emit has a
// catalog key — so adding an eleventh producer without a render entry fails HERE
// rather than on notification.events.dlq.inbox.
//
// The list is the ten build* methods on
// sale-service/app/Modules/PartsStorefront/Infrastructure/Outbox/PartsNotificationDirectiveBuilder.php
// plus the importer pair. It is hand-maintained on purpose: go-common cannot see
// sale-service, so the only honest alternative to a list is no check at all.
func TestPartsProducedTypesAreAllInTheCatalog(t *testing.T) {
	for _, tc := range partsLiveEnvelopes() {
		if _, ok := typeKey[tc.nt]; !ok {
			t.Errorf("%s: no catalog entry — every directive of this type dead-letters at inbox-service", tc.name)
		}
	}
}

// TestPartsUnmatchedPositionRendersWithoutADanglingName exercises the `{{else}}`
// half of every parts optional-param guard, which the fixtures above never reach
// because they all carry a name.
//
// IT IS NOT A CONTRIVED CASE. `PartsNotificationDirectiveBuilder` passes
// `$productName ?? ”` on six of the ten producible types, because a позиция the
// administrator has not yet matched to a card has no name anywhere in the
// storefront — the price file's «Наименование» cell is stored on no table — and
// the builder refuses to «echo the артикул as if it were a name the seller
// wrote». protojson then omits the empty string, so the field arrives ABSENT, and
// Render's optional-key fill is what stops missingkey=error turning that into a
// 500 inside the guard.
//
// The assertion is that the fallback clause is reached AND that no quote is left
// dangling — «” is visible to buyers again» is the exact defect the guard exists
// to prevent, and it renders perfectly happily if the guard is written on the
// wrong param.
func TestPartsUnmatchedPositionRendersWithoutADanglingName(t *testing.T) {
	r := testRendererFull(t)

	for _, tc := range partsLiveEnvelopes() {
		if !contains(optionalParams[tc.nt], "product_name") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			// Exactly what an unmatched позиция produces: protojson omits the
			// empty string, so the key never reaches the renderer at all.
			delete(params, "product_name")

			_, body, err := r.Render(tc.nt, params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(body, "''") {
				t.Errorf("dangling empty quotes — the guard is on the wrong param: %q", body)
			}
			t.Logf("unmatched → %s", body)
		})
	}
}
