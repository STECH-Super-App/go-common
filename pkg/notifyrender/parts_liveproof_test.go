package notifyrender

import (
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
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
// producer today; the next two are the price-file importer's pair, which had no
// producer when they landed; the last is Р44's return-terms clearing (172),
// whose producer — sale-service's return-terms moderation — waits for the
// gen-php-lib bump that carries the type.
//
// Values are taken from a real notification.events.dlq.inbox dump on the local
// compose stack (2026-08-31), not invented — that is what makes this a
// reproduction rather than a mock. They stay NAMELESS on purpose: that dump
// predates `shop_display_name`, and an empty name is still what a failed
// directory lookup sends. 172 has no dump to copy and carries the name its
// producer will send.
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
		{notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_RETURN_TERMS_CLEARED, "SHOP_RETURN_TERMS_CLEARED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendPartsShopReturnTermsCleared{
				SendPartsShopReturnTermsCleared: &notificationv1.SendPartsShopReturnTermsCleared{
					ShopDisplayName: "ООО «Ромашка-Запчасть»",
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
// plus the importer pair and 172. It is hand-maintained on purpose: go-common
// cannot see sale-service, so the only honest alternative to a list is no check
// at all.
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
// both shop-level lift texts, the two exclusive tails 79 gained on 07.09.2026,
// and the cases that make them worth a test.
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
// ⚠ «PRICE AGE AMONG OTHERS» IS THE WHOLE POINT of the upload tail. The obvious
// cheaper fix, `{{if eq .remaining_causes "PRICE_STALE"}}`, passes the
// single-cause case and FAILS that one: with two causes the joined string is
// «SHOP_PAUSED, PRICE_STALE», whole-string equality misses, and the seller
// silently loses the instruction — in exactly the multi-cause case the rule
// exists for. Membership, not exclusivity, is the reading for price age.
//
// 79's TWO NEW TAILS ARE THE OPPOSITE READING (vault «Уведомления запчастей»
// :132, owner ruling of 07.09.2026): «…когда вы возобновите продажи» and
// «…после разблокировки юрлица» each promise that ONE action brings the offers
// back, so they light only when that cause is the ONLY one left. A pause plus a
// legal-entity block gets the generic pointer, and price age still wins over
// both. 125 gains neither tail — a lone pause after a restored verification is
// still the generic pointer there.
//
// Every render lands on exactly one edition.
func TestPartsShopLiftTextsCarryTheB53Instruction(t *testing.T) {
	r := testRendererFull(t)

	const (
		sanctionLifted       = notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED
		verificationRestored = notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED
	)

	// The distinctive phrase of each edition, as the en baseline renders it.
	editions := map[string]string{
		"upload":    "upload a fresh price list",
		"resume":    "once you resume sales",
		"unblocked": "once your organisation is unblocked",
		"generic":   "another reason",
		"back":      "back in the catalogue",
	}

	cases := []struct {
		name   string
		causes []string
		// want is the one edition each type must render.
		want map[notificationv1.NotificationType]string
	}{
		{
			name:   "nothing left — the storefront really is back",
			causes: nil,
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "back", verificationRestored: "back"},
		},
		{
			name:   "the owner's own pause alone — 79's resume tail, 125's generic pointer",
			causes: []string{causeShopPaused},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "resume", verificationRestored: "generic"},
		},
		{
			// A repeated cause is still ONE cause: the distinct set decides.
			name:   "the same pause twice — still the resume tail",
			causes: []string{causeShopPaused, causeShopPaused},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "resume", verificationRestored: "generic"},
		},
		{
			name:   "the legal entity's block alone — 79's unblocked tail, 125's generic pointer",
			causes: []string{causeShopVerificationRevoked},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "unblocked", verificationRestored: "generic"},
		},
		{
			name:   "pause AND block — no single action brings the offers back, so the generic pointer",
			causes: []string{causeShopPaused, causeShopVerificationRevoked},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "generic", verificationRestored: "generic"},
		},
		{
			// An arm this build cannot name is still a cause the resume promise
			// would ignore.
			name:   "pause beside an unknown cause — the generic pointer",
			causes: []string{causeShopPaused, "SHOP_SOMETHING_NEW"},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "generic", verificationRestored: "generic"},
		},
		{
			name:   "the administrator's own hide left — the generic pointer",
			causes: []string{"SHOP_ADMIN_HIDDEN"},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "generic", verificationRestored: "generic"},
		},
		{
			name:   "price age alone — В-53's instruction",
			causes: []string{causePriceStale},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "upload", verificationRestored: "upload"},
		},
		{
			name:   "price age AMONG others — still the instruction",
			causes: []string{causeShopPaused, causePriceStale},
			want:   map[notificationv1.NotificationType]string{sanctionLifted: "upload", verificationRestored: "upload"},
		},
	}

	for _, nt := range []notificationv1.NotificationType{sanctionLifted, verificationRestored} {
		for _, tc := range cases {
			t.Run(typeKey[nt]+"/"+tc.name, func(t *testing.T) {
				env := envelopeWithCauses(nt, tc.causes)

				params, err := ExtractParams(env)
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}
				// 125's param set is unchanged; 79's carries the two tail flags.
				assertParamSetMatchesCatalog(t, nt, params)

				_, body, err := r.Render(nt, params, "en")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}

				var got []string
				for edition, phrase := range editions {
					if strings.Contains(body, phrase) {
						got = append(got, edition)
					}
				}
				// Exactly one edition, always — the five are mutually exclusive
				// by construction and a template edit must not make them overlap.
				if len(got) != 1 || got[0] != tc.want[nt] {
					t.Errorf("causes %v rendered editions %v, want exactly [%s]\n  body = %q",
						tc.causes, got, tc.want[nt], body)
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

// ───────────────────────── D-16a — the other forty-nine ─────────────────────────
//
// The fixtures above are a REPRODUCTION of directives that exist on the wire
// today, so they stop at the types a producer can emit. The tests below cover
// the set instead of the traffic: after D-16a and the two entries added on
// 07.09.2026, ALL SIXTY-THREE declared parts types render — sixty-four since 172.

// partsUnmappedByDesign are the parts types allowed to have no catalog entry.
// IT IS EMPTY, and that is the point of the 07.09.2026 pass: the last two —
// PARTS_ORDER_CONTACT_HANDOVER (123, Р47) and PARTS_SHOP_VERIFICATION_REVOKED
// (124, Р51) — are mapped, so every declared parts type renders.
//
// «The last two» describes that pass, not the frontier: PARTS_ORDER_CANCELLED_BY_ADMIN
// (133, М-20) was minted afterwards and mapped on 10.09.2026, taking the family
// to 64. The invariant below is what holds — EVERY declared type, whenever it
// arrives, not a fixed pair.
//
// The list survives its own emptying because it is the shape the exemption has
// to take if one is ever wanted again, and because an empty map states the
// invariant far more loudly than a deleted one: ADDING a line here is a
// REGRESSION. It would mean a NotificationType shipped without the text that
// makes it renderable, and an unmapped type does not skip — inbox-service wraps
// ErrUnknownType raw with no retry tier, so the directive dead-letters on FIRST
// delivery, and notifyoutbox rejects it inside the producer's transaction before
// that.
//
// ⚠ MAPPED IS NOT TRANSLATED. 124's Russian is still owed by the owner
// («Уведомления запчастей.md» carries no row for it in any of its five text
// tables), so a ru reader falls back to the English baseline until that sentence
// arrives. That is a translation debt, and it is deliberately NOT modelled as an
// exemption here: the type renders, which is what this file is about.
var partsUnmappedByDesign = map[notificationv1.NotificationType]string{}

// TestPartsCatalogCoversEveryDeclaredType walks the proto enum rather than a
// hand-written list, so it sees a sixty-fifth parts type the moment gen-go-lib
// carries one — which is exactly when somebody needs to be told that a template
// is owed before the producer lands (D-9's ordering gate).
func TestPartsCatalogCoversEveryDeclaredType(t *testing.T) {
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

	// 64 → 63 on 14.09.2026: value 112 (the «заявка продлена» directive) was retired
	// with its feature (В-64, В-94) and its number reserved. This pin moved with it.
	// 63 → 64 with proto push 1 (tracker wave, 10.2026): value 172,
	// PARTS_SHOP_RETURN_TERMS_CLEARED — Р44's moderator clearing of a shop's
	// additional return terms, the owner's late arm of 08.09.2026.
	if declared != 64 {
		t.Errorf("proto declares %d NOTIFICATION_TYPE_PARTS_* values, expected 64 — a type was added or removed, and this test is the place that has to notice", declared)
	}
	if len(partsUnmappedByDesign) != 0 {
		t.Errorf("partsUnmappedByDesign has %d entries, want 0 — every declared parts type has had a catalog entry since 07.09.2026, and a new exemption is a type that will dead-letter on first delivery", len(partsUnmappedByDesign))
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

// shopDisplayNameParam is the one param the owner ruling of 07.09.2026 added to
// the parts family, spelled once for every test that asks about it.
const shopDisplayNameParam = "shop_display_name"

// shopNamedOnlyTypes are the sixteen shop-subject texts whose ONLY optional param
// is the shop's display name — the twenty minus 69, 79, 121 and 130, which
// already carried an optional tier and had the name appended to it.
var shopNamedOnlyTypes = []notificationv1.NotificationType{
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_FILE_FAILED,               // 70
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_MATCHED,                    // 71
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_PUBLISHED_RETRO,            // 72
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_REJECTED,                   // 73
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_STALE_WARNING,             // 74
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFERS_HIDDEN_PRICE_LIST_STALE,       // 75
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_HIDDEN_BY_ADMIN,                 // 78
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFIED_BADGE_REVOKED,          // 80
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_RECEIVED,              // 104
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_INVITE,                        // 117
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_REMINDER,                      // 118
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_HIDDEN_BY_COMPLAINT,           // 120
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_UNAVAILABLE_ORDER_NOTICE,        // 126
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_SHOP_CLOSED, // 127
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_BY_SHOP,     // 128
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_RETURN_TERMS_CLEARED,            // 172
}

// shopSubjectPartsTypes is the whole twenty: the texts the owner ruling of
// 07.09.2026 («Уведомления запчастей» :75) names the shop on, because the shop
// is their subject.
func shopSubjectPartsTypes() []notificationv1.NotificationType {
	return append([]notificationv1.NotificationType{
		notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED,   // 69
		notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED,   // 79
		notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED, // 121
		notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK,    // 130
	}, shopNamedOnlyTypes...)
}

// payloadFieldFor is the envelope oneof arm that carries type nt, found by the
// fleet's naming rule — `send_` plus the type's name without its
// NOTIFICATION_TYPE_ prefix, lower-cased (notification-service's
// TestExpectedPayloadOneof_ConventionCensus holds every arm to it). Nil when no
// arm has that name.
func payloadFieldFor(nt notificationv1.NotificationType) protoreflect.FieldDescriptor {
	name := "send_" + strings.ToLower(strings.TrimPrefix(nt.String(), "NOTIFICATION_TYPE_"))
	return (&notificationv1.NotificationEnvelope{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
}

// TestShopSubjectPartsTextsNameTheShop is the owner ruling of 07.09.2026 as a
// structural lock, and the successor of B-5's TestNoPartsTemplateReadsAShopName.
//
// B-5 reserved `shop_name` on every SendParts* message and the old test proved no
// template could read one. The ruling did not undo that: it brought the name
// back as a NEW field, `shop_display_name`, on the twenty texts whose subject is
// the shop — so the lock now has four halves instead of two:
//
//   - the declaration: exactly the twenty parts types declare shop_display_name,
//     in the OPTIONAL tier, and no other parts type declares it or any param
//     spelled like `shop_name`;
//   - the wire: each of the twenty payloads has a string field shop_display_name,
//     and none of the sixty-four SendParts* payloads has a field named shop_name
//     — walked over the envelope's oneof, so a payload added later is checked
//     the moment it is generated;
//   - the extract: a payload carrying a name surfaces it under that param;
//   - the text: the English baseline prints it.
func TestShopSubjectPartsTextsNameTheShop(t *testing.T) {
	r := testRendererFull(t)
	const name = "Ромашка-Запчасть"

	subject := map[notificationv1.NotificationType]bool{}
	for _, nt := range shopSubjectPartsTypes() {
		subject[nt] = true
	}
	if len(subject) != 20 {
		t.Fatalf("shopSubjectPartsTypes lists %d distinct types, want the ruling's 20", len(subject))
	}

	// The declaration.
	for nt, key := range typeKey {
		if !strings.HasPrefix(key, "parts_") {
			continue
		}
		if got := contains(OptionalParams(nt), shopDisplayNameParam); got != subject[nt] {
			t.Errorf("%s declares optional shop_display_name = %v, want %v", key, got, subject[nt])
		}
		for _, p := range allowedParams(nt) {
			if strings.Contains(p, "shop_name") {
				t.Errorf("%s declares param %q — B-5 reserved shop_name, and the ruling of 07.09.2026 brought the name back as shop_display_name only", key, p)
			}
		}
	}

	// The wire: no payload offers shop_name, whatever the type.
	fields := (&notificationv1.NotificationEnvelope{}).ProtoReflect().Descriptor().Fields()
	walked := 0
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || !strings.HasPrefix(string(fd.Message().Name()), "SendParts") {
			continue
		}
		walked++
		if fd.Message().Fields().ByName("shop_name") != nil {
			t.Errorf("%s declares field shop_name — B-5 reserved it on every SendParts* message", fd.Message().Name())
		}
	}
	// 64 → 63 on 14.09.2026: envelope slot 131 (send_parts_sourcing_request_extended) was
	// retired with В-94 and reserved. 63 → 64 with proto push 1: slot 192,
	// send_parts_shop_return_terms_cleared (172, Р44). Same count as the
	// declared-types pin, on purpose.
	if walked != 64 {
		t.Errorf("walked %d SendParts* payloads, want 64 — the oneof changed shape and this check may be looking at nothing", walked)
	}

	// The wire, the extract and the text, per type.
	for _, nt := range shopSubjectPartsTypes() {
		key := typeKey[nt]
		t.Run(key, func(t *testing.T) {
			fd := payloadFieldFor(nt)
			if fd == nil {
				t.Fatalf("%s has no envelope arm named by the send_<type> rule", nt)
			}
			field := fd.Message().Fields().ByName(protoreflect.Name(shopDisplayNameParam))
			if field == nil || field.Kind() != protoreflect.StringKind || field.IsList() {
				t.Fatalf("%s carries no singular string field shop_display_name", fd.Message().Name())
			}

			env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: nt}}
			payload := env.ProtoReflect().Mutable(fd).Message()
			payload.Set(field, protoreflect.ValueOfString(name))

			params, err := ExtractParams(env)
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			if params[shopDisplayNameParam] != name {
				t.Fatalf("shop_display_name = %q, want %q — the arm does not read the field", params[shopDisplayNameParam], name)
			}
			_, body, err := r.Render(nt, params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(body, "'"+name+"'") {
				t.Errorf("the en body does not name the shop: %q", body)
			}
		})
	}
}

// todaysWording is every shop-subject text as it rendered BEFORE the ruling of
// 07.09.2026 reached the baseline, with each REQUIRED param at
// validParamsFor's value and every optional one absent — which is exactly what
// a directive whose producer could not resolve the name puts on the wire.
// Captured from the baseline at go-common 7710a15. 172 is not here: it had no
// text before.
var todaysWording = map[notificationv1.NotificationType]struct{ title, body string }{
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED: {"Price dropped",
		"An item in your favourites is cheaper: test_old_price → test_new_price ₽."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFERS_HIDDEN_PRICE_LIST_STALE: {"Offers hidden — the price list is out of date",
		"Your offers are hidden: the price list has not been updated for test_days_since_upload days. Upload a price list and they come back automatically."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK: {"Available again",
		"An item in your favourites is on sale again."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_MATCHED: {"Positions matched",
		"test_published_count more positions from your price list are published."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_PUBLISHED_RETRO: {"Positions published",
		"test_published_count more positions from your price list are published — an administrator matched them to catalogue cards."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_REJECTED: {"Positions rejected",
		"test_rejected_count positions from your price list were rejected during matching. The reasons are in the import report."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_FILE_FAILED: {"Price list not processed",
		"We could not process 'test_file_name'. Open the import report to see why, fix the file and upload it again."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED: {"Price list processed",
		"Your price list is processed: test_published_count published, test_matching_count awaiting matching, test_error_count with errors."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_STALE_WARNING: {"The price list is out of date",
		"Your price list has not been updated for test_days_since_upload days. Tomorrow your offers leave the catalogue — upload a fresh price list."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_HIDDEN_BY_COMPLAINT: {"Review hidden",
		"Your review of the shop was hidden by a moderator. Reason: test_reason."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_INVITE: {"Leave a review",
		"How was the shop? Leave a review for order #test_order_no."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_REMINDER: {"Review reminder",
		"2 days left to rate the shop for order #test_order_no."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_HIDDEN_BY_ADMIN: {"Shop hidden by an administrator",
		"Your shop was hidden by an administrator: your offers are not visible to buyers, and no new orders or sourcing requests reach you. Active orders are fulfilled under the usual rules. Reason: test_reason. Please write to support."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED: {"Sanction lifted",
		"Your shop is available to buyers again. Your offers are back in the catalogue."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_UNAVAILABLE_ORDER_NOTICE: {"The shop is temporarily unavailable",
		"The shop is temporarily unavailable on the platform. Order #test_order_no still stands; you can cancel it any time before it is handed to a carrier."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFIED_BADGE_REVOKED: {"The «VERIFIED» badge was removed",
		"The «VERIFIED» badge was removed from your shop. Questions go to support."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_RECEIVED: {"New quote on your request",
		"A new quote on request #test_request_no: test_position_count parts for test_total ₽."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_BY_SHOP: {"The shop withdrew its quote",
		"The shop withdrew its quote on request #test_request_no."},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_SHOP_CLOSED: {"The quote was withdrawn",
		"The shop closed — its quote on request #test_request_no was withdrawn."},
}

// TestShopNamedTextsKeepTodaysWordingWithoutAName pins the ruling's fallback: a
// directive whose producer could not resolve the shop's name — a tenant-service
// outage, an unknown tenant, a producer older than the field — renders the text
// that shipped before the name existed, byte for byte. The name is a decoration
// on a sentence that must already be whole without it.
//
// ONE DELIBERATE EXCEPTION. 72 (PARTS_POSITIONS_PUBLISHED_RETRO) drops «— an
// administrator matched them to catalogue cards» and becomes 71's sentence, as
// «Уведомления запчастей» :138 writes it; its golden is pinned to that new text
// instead. 79's new tails need causes to light and are
// TestPartsShopLiftTextsCarryTheB53Instruction's; with none, 79 is unchanged.
func TestShopNamedTextsKeepTodaysWordingWithoutAName(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay

	const retro = notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_PUBLISHED_RETRO
	want := map[notificationv1.NotificationType]struct{ title, body string }{}
	for nt, w := range todaysWording {
		want[nt] = w
	}
	want[retro] = struct{ title, body string }{"Positions published",
		"test_published_count more positions from your price list are published."}

	covered := 0
	for _, nt := range shopSubjectPartsTypes() {
		w, ok := want[nt]
		if !ok {
			continue // 172 — no text before the ruling.
		}
		covered++
		t.Run(typeKey[nt], func(t *testing.T) {
			title, body, err := r.Render(nt, validParamsFor(nt), "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if title != w.title {
				t.Errorf("title = %q, want today's %q", title, w.title)
			}
			if body != w.body {
				t.Errorf("body without a name drifted from today's wording\n  got  %q\n  want %q", body, w.body)
			}
		})
	}
	if covered != 19 {
		t.Errorf("pinned %d of the 19 shop-subject texts that existed before the ruling", covered)
	}
}
