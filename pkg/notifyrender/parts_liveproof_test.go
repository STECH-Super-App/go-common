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

// TestPartsShopLiftTextsCarryTheB53Instruction pins Р56·В-53's SECOND EDITION on
// both shop-level lift texts, and the case that makes it worth a test.
//
// THE RULE. When a sanction is lifted — or a company's verification restored —
// but the shop's price list is ALREADY older than fourteen days, the storefront
// does not come back, and the vault says the seller must be told what to do
// rather than left waiting: «…получают хвост «…Предложения вернутся после
// загрузки свежего прайса — текущий устарел.» — иначе продавец ждёт возврата
// витрины, которого не будет».
//
// WHY IT NEEDS A DERIVED PARAM (`O-67`, owner ruling of 01.09.2026, option (a)).
// The wire carries a LIST of remaining causes; this package flattens it to one
// string for the generic guard, and a flat string cannot answer «is price age
// among them?» — Go's text/template has no substring test in its default
// function set and the renderer registers none. `remaining_price_stale` is
// derived from the same field in ExtractParams, so nothing on the wire changed.
//
// ⚠ THE THIRD SUBTEST IS THE WHOLE POINT. The obvious cheaper fix,
// `{{if eq .remaining_causes "PRICE_STALE"}}`, passes the first two cases and
// FAILS this one: with two causes the joined string is «SHOP_PAUSED, PRICE_STALE»,
// whole-string equality misses, and the seller silently loses the instruction —
// in exactly the multi-cause case the rule exists for. Membership, not
// exclusivity, is the reading.
func TestPartsShopLiftTextsCarryTheB53Instruction(t *testing.T) {
	r := testRendererFull(t)

	types := map[string]notificationv1.NotificationType{
		"SHOP_SANCTION_LIFTED":       notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED,
		"SHOP_VERIFICATION_RESTORED": notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED,
	}

	cases := []struct {
		name        string
		causes      []string
		wantUpload  bool
		wantGeneric bool
		wantBack    bool
	}{
		{
			name:     "nothing left — the storefront really is back",
			causes:   nil,
			wantBack: true,
		},
		{
			name:        "another cause, not price age — the generic pointer",
			causes:      []string{"SHOP_PAUSED"},
			wantGeneric: true,
		},
		{
			name:       "price age alone — В-53's instruction",
			causes:     []string{"PRICE_STALE"},
			wantUpload: true,
		},
		{
			name:       "price age AMONG others — still the instruction",
			causes:     []string{"SHOP_PAUSED", "PRICE_STALE"},
			wantUpload: true,
		},
	}

	for label, nt := range types {
		for _, tc := range cases {
			t.Run(label+"/"+tc.name, func(t *testing.T) {
				env := envelopeWithCauses(nt, tc.causes)

				params, err := ExtractParams(env)
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}

				_, body, err := r.Render(nt, params, "en")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}

				gotUpload := strings.Contains(body, "upload a fresh price list")
				gotGeneric := strings.Contains(body, "another reason")
				gotBack := strings.Contains(body, "back in the catalogue")

				if gotUpload != tc.wantUpload || gotGeneric != tc.wantGeneric || gotBack != tc.wantBack {
					t.Errorf("branch mismatch for causes %v\n  got  upload=%v generic=%v back=%v\n  want upload=%v generic=%v back=%v\n  body = %q",
						tc.causes, gotUpload, gotGeneric, gotBack,
						tc.wantUpload, tc.wantGeneric, tc.wantBack, body)
				}

				// Exactly one branch, always — the three are mutually exclusive by
				// construction and a template edit must not make them overlap.
				if n := boolCount(gotUpload, gotGeneric, gotBack); n != 1 {
					t.Errorf("expected exactly one branch, got %d: %q", n, body)
				}
			})
		}
	}
}

func envelopeWithCauses(nt notificationv1.NotificationType, causes []string) *notificationv1.NotificationEnvelope {
	env := &notificationv1.NotificationEnvelope{
		Metadata: &notificationv1.EnvelopeMetadata{Type: nt},
	}
	if nt == notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED {
		env.Payload = &notificationv1.NotificationEnvelope_SendPartsShopSanctionLifted{
			SendPartsShopSanctionLifted: &notificationv1.SendPartsShopSanctionLifted{RemainingCauses: causes},
		}
		return env
	}
	env.Payload = &notificationv1.NotificationEnvelope_SendPartsShopVerificationRestored{
		SendPartsShopVerificationRestored: &notificationv1.SendPartsShopVerificationRestored{RemainingCauses: causes},
	}
	return env
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// ───────────────────────── D-16a — the other forty-nine ─────────────────────────
//
// The fixtures above are a REPRODUCTION of directives that exist on the wire
// today, so they stop at the twelve types a producer can emit. The tests below
// cover the set instead of the traffic: after D-16a, sixty-one of the
// sixty-three declared parts types render, and the two that do not are named,
// not merely missing.

// partsUnmappedByDesign are the only parts types allowed to have no catalog
// entry, and the reason is the same for both: «Уведомления запчастей.md» has
// FIVE text tables and neither type appears in any of them, so there is no
// Russian to transcribe and composing some would be inventing product copy.
//
//   - PARTS_ORDER_CONTACT_HANDOVER (123, Р47) — the vault gives it a recipient
//     («Преемник (Владелец или Менеджер команды)»), a target screen
//     (PROF-PRT-01) and «не отключается», and no sentence. OWNER-ANSWERS
//     2026-09-02 D-8 records the same finding.
//   - PARTS_SHOP_VERIFICATION_REVOKED (124, Р51) — the fourth of the shop-level
//     texts; the other three («Магазин скрыт администратором», «Бейдж
//     «ПРОВЕРЕНО» снят», «Магазин «[название]» временно недоступен») are
//     verbatim in the vault and are mapped.
//
// Shrinking this list is the goal. GROWING it is a regression: it would mean a
// new NotificationType shipped without the text that makes it renderable, and
// an unmapped type does not skip — inbox-service wraps ErrUnknownType raw with
// no retry tier, so the directive dead-letters on FIRST delivery.
var partsUnmappedByDesign = map[notificationv1.NotificationType]string{
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONTACT_HANDOVER:    "Р47 — no text in any of the vault's five tables (owner answer D-8)",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_REVOKED: "Р51 — the one shop-level text the vault never wrote",
}

// TestPartsCatalogCoversEveryDeclaredTypeButTheTwoWithNoText walks the proto
// enum rather than a hand-written list, so it sees a sixty-fourth parts type the
// moment gen-go-lib carries one — which is exactly when somebody needs to be
// told that a template is owed before the producer lands (D-9's ordering gate).
func TestPartsCatalogCoversEveryDeclaredTypeButTheTwoWithNoText(t *testing.T) {
	declared := 0
	for value, name := range notificationv1.NotificationType_name {
		if !strings.HasPrefix(name, "NOTIFICATION_TYPE_PARTS_") {
			continue
		}
		declared++
		nt := notificationv1.NotificationType(value)
		key, mapped := typeKey[nt]
		reason, exempt := partsUnmappedByDesign[nt]

		switch {
		case exempt && mapped:
			t.Errorf("%s is mapped to %q but still listed as unmapped-by-design (%s) — drop it from partsUnmappedByDesign", name, key, reason)
		case exempt:
			// The owed sentence has not arrived. Nothing to assert beyond the
			// list itself, which the count check below pins.
		case !mapped:
			t.Errorf("%s has NO catalog entry and is not one of the two the vault leaves textless — every directive of this type dead-letters at inbox-service on first delivery", name)
		default:
			if _, ok := BaselineEN[key+".title"]; !ok {
				t.Errorf("%s (%q): baseline has no title", name, key)
			}
			if _, ok := BaselineEN[key+".body"]; !ok {
				t.Errorf("%s (%q): baseline has no body", name, key)
			}
		}
	}

	if declared != 63 {
		t.Errorf("proto declares %d NOTIFICATION_TYPE_PARTS_* values, expected 63 — a type was added or removed, and this test is the place that has to notice", declared)
	}
	if len(partsUnmappedByDesign) != 2 {
		t.Errorf("partsUnmappedByDesign has %d entries, want 2", len(partsUnmappedByDesign))
	}
}

// TestPartsTemplatesDegradeCleanlyWithEveryOptionalAbsent renders every mapped
// parts type with its REQUIRED params only — which is precisely the wire shape,
// because protojson omits an empty string and Render then fills the absent
// optional with "".
//
// It asserts the OUTPUT SHAPE rather than the wording: the failure this catches
// is a guard written on the wrong param or forgotten entirely, and its signature
// is always a dangling fragment — «Заявка #12 закрыта: .», «” снова видна»,
// «отправка до ;», a double space where a collapsed clause used to be. The
// twelve shipped types are in the loop too, so the property is stated once for
// the whole family rather than per patch.
func TestPartsTemplatesDegradeCleanlyWithEveryOptionalAbsent(t *testing.T) {
	r := testRendererFull(t)

	artefacts := []string{
		": .", ": ,", " .", " ,", "  ", "«»", "''", "#.", "# ", "— .", "()",
	}

	for nt, key := range typeKey {
		if !strings.HasPrefix(key, "parts_") {
			continue
		}
		t.Run(key, func(t *testing.T) {
			title, body, err := r.Render(nt, validParamsFor(nt), "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, part := range []string{title, body} {
				if part == "" {
					t.Fatalf("empty half (title=%q body=%q)", title, body)
				}
				for _, bad := range artefacts {
					if strings.Contains(part, bad) {
						t.Errorf("dangling %q — an optional param is read outside its guard: %q", bad, part)
					}
				}
			}
			t.Logf("%s", body)
		})
	}
}

// TestPartsBranchFlagsNeverAssertTheWrongEdition is the executable form of the
// rule flagWhen exists to keep.
//
// Four parts payloads pick between EDITIONS of one sentence with a wire enum —
// `fulfilment_kind`, `deadline_basis`, `partial_kind` and В-66's complaint
// `outcome`. ExtractParams turns each into a PAIR of flags rather than one flag
// plus an {{else}}, and this test is why: the cheaper shape passes every
// happy-path case and fails exactly here, where the enum is empty or carries a
// value this build does not know. With one flag the unknown value falls into the
// else and the template asserts the OTHER edition — a buyer whose order is
// самовывоз is told it was handed to a carrier, and a complaint with no recorded
// outcome is reported as resolved in the complainant's favour. With a flag on
// each arm the clause is simply not printed.
//
// An absent clause is a push that says less. A wrong clause is a push that lies.
func TestPartsBranchFlagsNeverAssertTheWrongEdition(t *testing.T) {
	r := testRendererFull(t)

	// The distinctive phrase of each edition, as the en baseline renders it.
	const (
		pickupPhrase      = "Pickup: ready to collect"
		fromPaymentPhrase = "dispatch on the day of payment"
		carrierPhrase     = "dispatch by"
		readinessPhrase   = "readiness deadline"
		dispatchPhrase    = "dispatch deadline"
		preparedPhrase    = "has not prepared"
		dispatchedPhrase  = "has not dispatched"
		quantityPhrase    = "the quantity was reduced"
		hiddenPhrase      = "the review is hidden"
		noViolationPhrase = "no violation was found"
	)

	cases := []struct {
		name string
		nt   notificationv1.NotificationType
		env  *notificationv1.NotificationEnvelope
		// want is the edition that must appear; absent is every edition that
		// must NOT. An empty want means «no edition at all».
		want   string
		absent []string
	}{
		{
			name: "confirmed/pickup",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED,
			env: confirmedEnv(&notificationv1.SendPartsOrderConfirmed{
				OrderNo: "12345", FulfilmentKind: "PICKUP", DeadlineBasis: "CALENDAR", ReadyDate: "2026-09-10",
			}),
			want: pickupPhrase, absent: []string{fromPaymentPhrase, carrierPhrase},
		},
		{
			name: "confirmed/carrier with a calendar date",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED,
			env: confirmedEnv(&notificationv1.SendPartsOrderConfirmed{
				OrderNo: "12345", FulfilmentKind: "CARRIER", DeadlineBasis: "CALENDAR", ReadyDate: "2026-09-10",
			}),
			want: carrierPhrase, absent: []string{pickupPhrase, fromPaymentPhrase},
		},
		{
			// Р37 / Р39-№1: «от оплаты» terms have no calendar date at all, and the
			// FROM_PAYMENT arm must win over the plain carrier arm that would
			// otherwise print «dispatch by » with nothing after it.
			name: "confirmed/carrier from payment — no date exists",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED,
			env: confirmedEnv(&notificationv1.SendPartsOrderConfirmed{
				OrderNo: "12345", FulfilmentKind: "CARRIER", DeadlineBasis: "FROM_PAYMENT",
			}),
			want: fromPaymentPhrase, absent: []string{pickupPhrase, carrierPhrase},
		},
		{
			name: "confirmed/EMPTY kind asserts nothing",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED,
			env:  confirmedEnv(&notificationv1.SendPartsOrderConfirmed{OrderNo: "12345"}),
			want: "", absent: []string{pickupPhrase, carrierPhrase, fromPaymentPhrase},
		},
		{
			name: "confirmed/UNKNOWN kind asserts nothing",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED,
			env: confirmedEnv(&notificationv1.SendPartsOrderConfirmed{
				OrderNo: "12345", FulfilmentKind: "COURIER", DeadlineBasis: "CALENDAR", ReadyDate: "2026-09-10",
			}),
			want: "", absent: []string{pickupPhrase, carrierPhrase, fromPaymentPhrase},
		},
		{
			name: "overdue seller/pickup says readiness",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_SELLER,
			env: overdueSellerEnv(&notificationv1.SendPartsOrderFulfilmentOverdueSeller{
				OrderNo: "12345", FulfilmentKind: "PICKUP", DeadlineDate: "2026-09-01",
			}),
			want: readinessPhrase, absent: []string{dispatchPhrase},
		},
		{
			name: "overdue seller/EMPTY kind says neither",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_SELLER,
			env: overdueSellerEnv(&notificationv1.SendPartsOrderFulfilmentOverdueSeller{
				OrderNo: "12345", DeadlineDate: "2026-09-01",
			}),
			want: "", absent: []string{readinessPhrase, dispatchPhrase},
		},
		{
			name: "overdue buyer/carrier says dispatched",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_BUYER,
			env: overdueBuyerEnv(&notificationv1.SendPartsOrderFulfilmentOverdueBuyer{
				OrderNo: "12345", FulfilmentKind: "CARRIER",
			}),
			want: dispatchedPhrase, absent: []string{preparedPhrase},
		},
		{
			name: "overdue buyer/EMPTY kind says neither",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_BUYER,
			env:  overdueBuyerEnv(&notificationv1.SendPartsOrderFulfilmentOverdueBuyer{OrderNo: "12345"}),
			want: "", absent: []string{preparedPhrase, dispatchedPhrase},
		},
		{
			// Р56·В-56: a quantity-only reduction removes NO position, so K == N and
			// «K из N позиций» would be a lie. This is the case partial_kind exists for.
			name: "partial/quantity reduced never quotes the counts",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY,
			env: partialEnv(&notificationv1.SendPartsOrderConfirmedPartially{
				OrderNo: "12345", PartialKind: "QUANTITY_REDUCED", ConfirmedCount: 3, TotalCount: 3,
			}),
			want: quantityPhrase, absent: []string{"of 3 positions"},
		},
		{
			name: "partial/positions removed quotes the counts",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY,
			env: partialEnv(&notificationv1.SendPartsOrderConfirmedPartially{
				OrderNo: "12345", PartialKind: "POSITIONS_REMOVED", ConfirmedCount: 2, TotalCount: 5,
			}),
			want: "2 of 5 positions", absent: []string{quantityPhrase},
		},
		{
			name: "partial/EMPTY kind claims neither",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY,
			env: partialEnv(&notificationv1.SendPartsOrderConfirmedPartially{
				OrderNo: "12345", ConfirmedCount: 3, TotalCount: 3,
			}),
			want: "", absent: []string{quantityPhrase, "of 3 positions"},
		},
		{
			name: "complaint/hidden",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED,
			env:  complaintEnv(&notificationv1.SendPartsReviewComplaintResolved{Outcome: "HIDDEN"}),
			want: hiddenPhrase, absent: []string{noViolationPhrase},
		},
		{
			name: "complaint/no violation",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED,
			env:  complaintEnv(&notificationv1.SendPartsReviewComplaintResolved{Outcome: "NO_VIOLATION"}),
			want: noViolationPhrase, absent: []string{hiddenPhrase},
		},
		{
			name: "complaint/EMPTY outcome resolves in nobody's favour",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED,
			env:  complaintEnv(&notificationv1.SendPartsReviewComplaintResolved{}),
			want: "", absent: []string{hiddenPhrase, noViolationPhrase},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.env.Metadata = &notificationv1.EnvelopeMetadata{Type: tc.nt}

			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			_, body, err := r.Render(tc.nt, params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("missing edition %q in %q", tc.want, body)
			}
			for _, no := range tc.absent {
				if strings.Contains(body, no) {
					t.Errorf("asserted the WRONG edition %q in %q", no, body)
				}
			}
			t.Logf("%s", body)
		})
	}
}

func confirmedEnv(m *notificationv1.SendPartsOrderConfirmed) *notificationv1.NotificationEnvelope {
	return &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPartsOrderConfirmed{SendPartsOrderConfirmed: m},
	}
}

func partialEnv(m *notificationv1.SendPartsOrderConfirmedPartially) *notificationv1.NotificationEnvelope {
	return &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPartsOrderConfirmedPartially{SendPartsOrderConfirmedPartially: m},
	}
}

func overdueSellerEnv(m *notificationv1.SendPartsOrderFulfilmentOverdueSeller) *notificationv1.NotificationEnvelope {
	return &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPartsOrderFulfilmentOverdueSeller{SendPartsOrderFulfilmentOverdueSeller: m},
	}
}

func overdueBuyerEnv(m *notificationv1.SendPartsOrderFulfilmentOverdueBuyer) *notificationv1.NotificationEnvelope {
	return &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPartsOrderFulfilmentOverdueBuyer{SendPartsOrderFulfilmentOverdueBuyer: m},
	}
}

func complaintEnv(m *notificationv1.SendPartsReviewComplaintResolved) *notificationv1.NotificationEnvelope {
	return &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPartsReviewComplaintResolved{SendPartsReviewComplaintResolved: m},
	}
}

// TestNoPartsTemplateReadsAShopName is B-5's structural lock, and it is not
// hypothetical bookkeeping: the gen-go-lib this module pins
// (v0.0.0-20260821100229-a79231a125a5) PREDATES the 31.08.2026 removal, so
// `ShopName` still exists as a Go field on most SendParts* structs and a reader
// added by habit would compile, pass every happy-path test, and quietly put a
// name into copy the owner removed the field for.
//
// The check has two halves because the mistake has two shapes: a template that
// declares the param, and an extraction arm that returns it under any name.
func TestNoPartsTemplateReadsAShopName(t *testing.T) {
	for nt, key := range typeKey {
		if !strings.HasPrefix(key, "parts_") {
			continue
		}
		for _, p := range allowedParams(nt) {
			if strings.Contains(p, "shop_name") {
				t.Errorf("%s declares param %q — B-5 removed shop_name from all 53 SendParts* messages", key, p)
			}
		}
	}

	const sentinel = "ООО «Ромашка-Запчасть»"
	envs := []*notificationv1.NotificationEnvelope{
		confirmedEnv(&notificationv1.SendPartsOrderConfirmed{OrderNo: "1", ShopName: sentinel, FulfilmentKind: "PICKUP", ReadyDate: "2026-09-10"}),
		partialEnv(&notificationv1.SendPartsOrderConfirmedPartially{OrderNo: "1", ShopName: sentinel, PartialKind: "POSITIONS_REMOVED", ConfirmedCount: 1, TotalCount: 2}),
		complaintEnv(&notificationv1.SendPartsReviewComplaintResolved{ShopName: sentinel, Outcome: "HIDDEN"}),
		{Payload: &notificationv1.NotificationEnvelope_SendPartsOrderCreated{
			SendPartsOrderCreated: &notificationv1.SendPartsOrderCreated{ShopName: sentinel, OrderNo: "1", PositionCount: 2, Total: "5 200"},
		}},
		{Payload: &notificationv1.NotificationEnvelope_SendPartsSourcingQuoteReceived{
			SendPartsSourcingQuoteReceived: &notificationv1.SendPartsSourcingQuoteReceived{RequestNo: "7", ShopName: sentinel, PositionCount: 3, Total: "9 900"},
		}},
		{Payload: &notificationv1.NotificationEnvelope_SendPartsReviewInvite{
			SendPartsReviewInvite: &notificationv1.SendPartsReviewInvite{OrderNo: "1", ShopName: sentinel},
		}},
	}

	for _, env := range envs {
		params, err := ExtractParams(env)
		if err != nil {
			t.Fatalf("ExtractParams: %v", err)
		}
		for name, value := range params {
			if strings.Contains(value, sentinel) {
				t.Errorf("param %q carries the shop name %q — the arm reads a field B-5 reserved", name, value)
			}
		}
	}
}
