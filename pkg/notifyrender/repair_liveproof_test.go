package notifyrender

import (
	"errors"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// repairDealTypes is the order-side family of master §3.3 (rows 0–22, enum
// values 134–156): every directive order-service emits off a REPAIR deal.
//
// It is a hand-written list on purpose. go-common cannot see order-service, so
// the only honest alternative to naming the 23 types here is no check at all —
// and the failure this list catches is the one that matters: a type with a
// producer and no catalog entry does not degrade to «no push», it rolls the
// producer's own transaction back (notifyoutbox.PublishDirective validates
// inside the caller's tx, notifyoutbox.go:115-147).
var repairDealTypes = []notificationv1.NotificationType{
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CREATED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_SENT,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_ACCEPTED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_DECLINED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_WITHDRAWN,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_REJECTED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED_PROVIDER,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_WORK_STARTED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_PROPOSED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_ACCEPTED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_DECLINED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_WITHDRAWN,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_WORK_COMPLETED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_CONFIRM_REMINDER,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_AUTO_CONFIRMED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_COMPLETED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_RECEIVED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_AUTO_CANCELLED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_INVITE,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_WINDOW_ENDING,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_PAIR_FORMED,
}

// TestRepairDealCatalogIsComplete asserts the four coupled edits landed for
// every order-side type: a typeKey section, a required-param contract, and both
// halves of the English baseline.
func TestRepairDealCatalogIsComplete(t *testing.T) {
	if len(repairDealTypes) != 23 {
		t.Fatalf("repairDealTypes has %d entries, want 23 (master §3.3 rows 0–22)", len(repairDealTypes))
	}
	for _, nt := range repairDealTypes {
		key, ok := typeKey[nt]
		if !ok {
			t.Errorf("%s: no typeKey entry — every directive of this type rolls back its producer's transaction", nt)
			continue
		}
		if !strings.HasPrefix(key, "repair_") {
			t.Errorf("%s: section %q does not use the repair_ prefix", nt, key)
		}
		if len(requiredParams[nt]) == 0 {
			t.Errorf("%s (%q): no requiredParams contract", nt, key)
		}
		if _, ok := BaselineEN[key+".title"]; !ok {
			t.Errorf("%s (%q): baseline has no title", nt, key)
		}
		if _, ok := BaselineEN[key+".body"]; !ok {
			t.Errorf("%s (%q): baseline has no body", nt, key)
		}
	}
}

// repairBankTypes is the sale-side family of master §3.3 (rows 23–30, enum
// values 157–164): the eight directives sale-service's Repair module emits off
// a posting or a response. Every one of them is tenant-addressed — to the
// customer tenant or to a seller tenant, one directive per tenant (spec §11).
var repairBankTypes = []notificationv1.NotificationType{
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_MATCHING_POSTING,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_RECEIVED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_WITHDRAWN,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_DECLINED,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRING,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_CUSTOMER,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_RESPONDER,
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_CANCELLED,
}

// TestRepairCatalogCoversEveryDeclaredType walks the proto enum instead of the
// two lists above, so it sees a thirty-second repair type the moment gen-go-lib
// carries one — which is exactly when somebody needs to be told that a template
// is owed BEFORE the producer lands (D-9's ordering gate, and the reason
// notifyoutbox validates inside the caller's transaction).
func TestRepairCatalogCoversEveryDeclaredType(t *testing.T) {
	declared := 0
	for value, name := range notificationv1.NotificationType_name {
		if !strings.HasPrefix(name, "NOTIFICATION_TYPE_REPAIR_") {
			continue
		}
		declared++
		nt := notificationv1.NotificationType(value)
		key, mapped := typeKey[nt]
		if !mapped {
			t.Errorf("%s has NO catalog entry — a producer emitting it would roll its own transaction back", name)
			continue
		}
		if _, ok := BaselineEN[key+".title"]; !ok {
			t.Errorf("%s (%q): baseline has no title", name, key)
		}
		if _, ok := BaselineEN[key+".body"]; !ok {
			t.Errorf("%s (%q): baseline has no body", name, key)
		}
	}
	if declared != 31 {
		t.Errorf("proto declares %d NOTIFICATION_TYPE_REPAIR_* values, expected 31 (master §3.3, values 134–164) — a type was added or removed, and this test is the place that has to notice", declared)
	}
	if got := len(repairDealTypes) + len(repairBankTypes); got != declared {
		t.Errorf("the two hand-written family lists cover %d types, the proto declares %d", got, declared)
	}
}

// repairCase is one directive as it appears on the wire: the type its metadata
// carries and the payload arm its producer sets.
type repairCase struct {
	nt   notificationv1.NotificationType
	name string
	env  *notificationv1.NotificationEnvelope
}

// stamp puts the metadata type on each envelope. ExtractParams reads it to NAME
// the type in ErrUnknownType, so without it a failure would read
// NOTIFICATION_TYPE_UNSPECIFIED and stop matching the line inbox-service
// actually writes to notification.events.dlq.inbox.
func stamp(cases []repairCase) []repairCase {
	for i := range cases {
		cases[i].env.Metadata = &notificationv1.EnvelopeMetadata{Type: cases[i].nt}
	}
	return cases
}

// repairDealEnvelopes mirrors, field for field, the directives order-service's
// REPAIR vertical writes into order_service.outbox_messages. Values are the
// vault's own examples («Уведомления ремонта.md») rather than lorem: the
// machinery label is the frozen «<тип> <марка> <модель>», the offer and price
// are pre-formatted, work_types is already joined.
func repairDealEnvelopes() []repairCase {
	return stamp([]repairCase{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CREATED, "REQUEST_CREATED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestCreated{
				SendRepairRequestCreated: &notificationv1.SendRepairRequestCreated{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200",
					WorkTypes: "Гидравлика, Двигатель", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_SENT, "OFFER_SENT", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairOfferSent{
				SendRepairOfferSent: &notificationv1.SendRepairOfferSent{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200",
					Offer: "12 500 ₽", SupersedesPrevious: "false", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_ACCEPTED, "OFFER_ACCEPTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairOfferAccepted{
				SendRepairOfferAccepted: &notificationv1.SendRepairOfferAccepted{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_DECLINED, "OFFER_DECLINED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairOfferDeclined{
				SendRepairOfferDeclined: &notificationv1.SendRepairOfferDeclined{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_WITHDRAWN, "OFFER_WITHDRAWN", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairOfferWithdrawn{
				SendRepairOfferWithdrawn: &notificationv1.SendRepairOfferWithdrawn{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_REJECTED, "REQUEST_REJECTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestRejected{
				SendRepairRequestRejected: &notificationv1.SendRepairRequestRejected{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200",
					Reason: "Нет свободных мощностей", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED, "REQUEST_EXPIRED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestExpired{
				SendRepairRequestExpired: &notificationv1.SendRepairRequestExpired{
					Machinery: "Экскаватор Komatsu PC200", HadOffer: "true",
					ProviderName: "СТО «Гидромаш»", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED_PROVIDER, "REQUEST_EXPIRED_PROVIDER", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestExpiredProvider{
				SendRepairRequestExpiredProvider: &notificationv1.SendRepairRequestExpiredProvider{
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_WORK_STARTED, "WORK_STARTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairWorkStarted{
				SendRepairWorkStarted: &notificationv1.SendRepairWorkStarted{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_PROPOSED, "NEW_PRICE_PROPOSED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairNewPriceProposed{
				SendRepairNewPriceProposed: &notificationv1.SendRepairNewPriceProposed{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200",
					Price: "18 000 ₽", SupersedesPrevious: "true", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_ACCEPTED, "NEW_PRICE_ACCEPTED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairNewPriceAccepted{
				SendRepairNewPriceAccepted: &notificationv1.SendRepairNewPriceAccepted{
					CustomerName: "ООО «Стройтранс»", Price: "18 000 ₽",
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_DECLINED, "NEW_PRICE_DECLINED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairNewPriceDeclined{
				SendRepairNewPriceDeclined: &notificationv1.SendRepairNewPriceDeclined{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_WITHDRAWN, "NEW_PRICE_WITHDRAWN", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairNewPriceWithdrawn{
				SendRepairNewPriceWithdrawn: &notificationv1.SendRepairNewPriceWithdrawn{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_WORK_COMPLETED, "WORK_COMPLETED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairWorkCompleted{
				SendRepairWorkCompleted: &notificationv1.SendRepairWorkCompleted{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_CONFIRM_REMINDER, "CONFIRM_REMINDER", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairConfirmReminder{
				SendRepairConfirmReminder: &notificationv1.SendRepairConfirmReminder{
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_AUTO_CONFIRMED, "AUTO_CONFIRMED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairAutoConfirmed{
				SendRepairAutoConfirmed: &notificationv1.SendRepairAutoConfirmed{
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_COMPLETED, "REQUEST_COMPLETED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestCompleted{
				SendRepairRequestCompleted: &notificationv1.SendRepairRequestCompleted{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200",
					Price: "18 000 ₽", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_RECEIVED, "REVIEW_RECEIVED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairReviewReceived{
				SendRepairReviewReceived: &notificationv1.SendRepairReviewReceived{
					CustomerName: "ООО «Стройтранс»", Machinery: "Экскаватор Komatsu PC200",
					Rating: 5, DealCompleted: "true", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED, "REQUEST_CANCELLED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestCancelled{
				SendRepairRequestCancelled: &notificationv1.SendRepairRequestCancelled{
					CancelledBy: "customer", ActorName: "ООО «Стройтранс»",
					Machinery: "Экскаватор Komatsu PC200", Reason: "Ремонт больше не нужен", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_AUTO_CANCELLED, "REQUEST_AUTO_CANCELLED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairRequestAutoCancelled{
				SendRepairRequestAutoCancelled: &notificationv1.SendRepairRequestAutoCancelled{
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_INVITE, "REVIEW_INVITE", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairReviewInvite{
				SendRepairReviewInvite: &notificationv1.SendRepairReviewInvite{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_WINDOW_ENDING, "REVIEW_WINDOW_ENDING", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairReviewWindowEnding{
				SendRepairReviewWindowEnding: &notificationv1.SendRepairReviewWindowEnding{
					ProviderName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_PAIR_FORMED, "PAIR_FORMED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairPairFormed{
				SendRepairPairFormed: &notificationv1.SendRepairPairFormed{
					Machinery: "Экскаватор Komatsu PC200", RequestNo: "1042",
				}}}},
	})
}

// TestRepairDirectivesRenderRatherThanDeadLetter is the executable form of the
// question «does a repair directive render, or does it dead-letter?». The
// failure it catches is byte-identical to the one inbox-service writes onto
// notification.events.dlq.inbox — `extract params: unknown notification type …`
// — because inbox-service calls this exact ExtractParams and wraps its error
// raw (inbox-service/internal/application/ingestion/service.go:120).
func TestRepairDirectivesRenderRatherThanDeadLetter(t *testing.T) {
	r := testRendererFull(t)

	for _, tc := range repairEnvelopes() {
		t.Run(tc.name, func(t *testing.T) {
			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("DEAD LETTER — ExtractParams: %v", err)
			}
			assertParamSetMatchesCatalog(t, tc.nt, params)
			for _, p := range RequiredParams(tc.nt) {
				if params[p] == "" {
					t.Errorf("required param %q is empty — notifyoutbox rejects this directive inside the producer's transaction", p)
				}
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

// repairBankEnvelopes mirrors the directives sale-service's Repair module
// writes into repair_service.outbox_messages, tenant-addressed per §11.
//
// The values are the vault's own examples, and they are deliberately the SAME
// machinery label the deal-side fixtures carry: one posting becomes one deal,
// and the label is frozen at posting time, so a divergence here would be a
// fixture telling a story the product cannot.
func repairBankEnvelopes() []repairCase {
	return stamp([]repairCase{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_MATCHING_POSTING, "MATCHING_POSTING", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairMatchingPosting{
				SendRepairMatchingPosting: &notificationv1.SendRepairMatchingPosting{
					Machinery: "Экскаватор Komatsu PC200", WorkTypes: "Гидравлика, Двигатель", DistanceKm: 24,
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_RECEIVED, "RESPONSE_RECEIVED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairResponseReceived{
				SendRepairResponseReceived: &notificationv1.SendRepairResponseReceived{
					Machinery: "Экскаватор Komatsu PC200", Offer: "10 000–15 000 ₽",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_WITHDRAWN, "RESPONSE_WITHDRAWN", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairResponseWithdrawn{
				SendRepairResponseWithdrawn: &notificationv1.SendRepairResponseWithdrawn{
					SellerName: "СТО «Гидромаш»", Machinery: "Экскаватор Komatsu PC200",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_DECLINED, "RESPONSE_DECLINED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairResponseDeclined{
				SendRepairResponseDeclined: &notificationv1.SendRepairResponseDeclined{
					Machinery: "Экскаватор Komatsu PC200",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRING, "POSTING_EXPIRING", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairPostingExpiring{
				SendRepairPostingExpiring: &notificationv1.SendRepairPostingExpiring{
					Machinery: "Экскаватор Komatsu PC200",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_CUSTOMER, "POSTING_EXPIRED_CUSTOMER", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairPostingExpiredCustomer{
				SendRepairPostingExpiredCustomer: &notificationv1.SendRepairPostingExpiredCustomer{
					Machinery: "Экскаватор Komatsu PC200",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_RESPONDER, "POSTING_EXPIRED_RESPONDER", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairPostingExpiredResponder{
				SendRepairPostingExpiredResponder: &notificationv1.SendRepairPostingExpiredResponder{
					Machinery: "Экскаватор Komatsu PC200",
				}}}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_CANCELLED, "POSTING_CANCELLED", &notificationv1.NotificationEnvelope{
			Payload: &notificationv1.NotificationEnvelope_SendRepairPostingCancelled{
				SendRepairPostingCancelled: &notificationv1.SendRepairPostingCancelled{
					Machinery: "Экскаватор Komatsu PC200",
				}}}},
	})
}

// repairEnvelopes is every repair directive on the wire, both producers.
func repairEnvelopes() []repairCase {
	return append(repairDealEnvelopes(), repairBankEnvelopes()...)
}

// repairTypes is every repair type master §3.3 declares, both families, in row
// order. TestRepairCatalogCoversEveryDeclaredType is what pins this union to
// the proto enum; everything below may therefore treat it as «the 31».
func repairTypes() []notificationv1.NotificationType {
	all := make([]notificationv1.NotificationType, 0, len(repairDealTypes)+len(repairBankTypes))
	all = append(all, repairDealTypes...)
	return append(all, repairBankTypes...)
}

// TestRepairEveryMappedTypeHasAnExtractArm is the half the two fixture lists
// cannot cover: they would still pass if somebody deleted a case, because a
// missing arm is simply a missing fixture. This asserts the SET — so a
// thirty-second type fails HERE rather than on notification.events.dlq.inbox.
//
// IT TIES THE TWO INDEXES BOTH WAYS, and each direction catches a different
// mistake:
//
//   - forward — every one of the 31 declared types has EXACTLY ONE wire fixture
//     and an ExtractParams arm that claims it. One fixture, not «at least one»:
//     a duplicated case would keep the totals right while another type sits
//     uncovered.
//   - reverse — every fixture's type is one of the 31, so a fixture cannot be
//     parked against a type no family list declares and quietly inflate the
//     render loop's subtest count.
//
// The arm check reads the REASON rather than merely «err != nil»: an unmapped
// payload and a broken one are different failures, and only the first is the
// one this test exists to name.
func TestRepairEveryMappedTypeHasAnExtractArm(t *testing.T) {
	fixtures := map[notificationv1.NotificationType][]repairCase{}
	for _, tc := range repairEnvelopes() {
		fixtures[tc.nt] = append(fixtures[tc.nt], tc)
	}

	declared := map[notificationv1.NotificationType]bool{}
	for _, nt := range repairTypes() {
		declared[nt] = true
		key := typeKey[nt]
		cases := fixtures[nt]
		switch {
		case len(cases) == 0:
			t.Errorf("%s (%q) is a declared repair type with no wire fixture — nothing proves its ExtractParams arm exists", nt, key)
			continue
		case len(cases) > 1:
			t.Errorf("%s (%q) has %d fixtures (%v) — one type, one fixture, or a duplicate hides an uncovered type behind the right total", nt, key, len(cases), caseNames(cases))
		}

		if _, err := ExtractParams(cases[0].env); err != nil {
			var appErr *commonerr.AppError
			if errors.As(err, &appErr) && appErr.Reason == ReasonUnknownType {
				t.Errorf("%s (%q) has a catalog entry and a fixture but NO ExtractParams arm — every directive of this type dead-letters on notification.events.dlq.inbox", nt, key)
				continue
			}
			t.Errorf("%s (%q): ExtractParams failed for a reason other than a missing arm: %v", nt, key, err)
		}
	}

	for nt, cases := range fixtures {
		if !declared[nt] {
			t.Errorf("fixture(s) %v carry type %s, which neither repairDealTypes nor repairBankTypes declares", caseNames(cases), nt)
		}
	}

	if len(declared) != 31 {
		t.Errorf("the two family lists declare %d distinct types, want 31 (master §3.3, values 134–164)", len(declared))
	}

	// The catalog's own index, walked independently: a repair_ section that no
	// family list names would otherwise never be asked for a fixture.
	for nt, key := range typeKey {
		if !strings.HasPrefix(key, "repair_") {
			continue
		}
		if !declared[nt] {
			t.Errorf("%s (%q) has a catalog entry but appears in neither family list", nt, key)
		}
	}
}

// caseNames is the subtest names of a group of fixtures, for a failure that has
// to say WHICH duplicates it found.
func caseNames(cases []repairCase) []string {
	names := make([]string, 0, len(cases))
	for _, tc := range cases {
		names = append(names, tc.name)
	}
	return names
}

// assertParamSetMatchesCatalog asserts ExtractParams emits EXACTLY the keys the
// catalog declares for nt — required plus optional, no more and no less.
//
// WHY RENDERING CANNOT ANSWER THIS. text/template ignores a key nobody reads, so
// an arm that emits a param under the wrong name (or a leftover from the case it
// was copied from) renders perfectly and forever. The reverse is just as quiet
// for the optional tier: a dropped key and an empty value are the same thing to
// an {{if}} guard, so a decoration that was supposed to appear simply never does
// and every test still passes. Only the required tier is self-policing — Render
// rejects a missing one — and that is precisely the half that does NOT need a
// test. So this compares the SETS, through the package's public accessors, which
// are the same two lists i18n-catalog's validator holds a translation to.
func assertParamSetMatchesCatalog(t *testing.T, nt notificationv1.NotificationType, params map[string]string) {
	t.Helper()
	declared := map[string]bool{}
	for _, p := range RequiredParams(nt) {
		declared[p] = true
	}
	for _, p := range OptionalParams(nt) {
		declared[p] = true
	}

	for p := range params {
		if !declared[p] {
			t.Errorf("ExtractParams emits %q, which the catalog declares neither required nor optional for %s — Render ignores an unread key, so nothing else can notice", p, nt)
		}
	}
	for p := range declared {
		if _, ok := params[p]; !ok {
			t.Errorf("the catalog declares %q for %s but ExtractParams never emits that key — an optional one then collapses its clause forever, silently", p, nt)
		}
	}
}

// TestRepairMatchingPostingDropsAZeroDistance is the executable form of the
// promise three places in this package already make in prose — catalog.go's
// optionalParams note, baseline.go's «{{if .distance_km}}» guard, and
// notifyrender_test.go's optional-tier census — and which nothing until now
// held the arm to.
//
// A zero is a REAL value on Р26's fan-out: a seller standing on the posting's
// own point is the best match there is, not a missing measurement. It must
// therefore drop the decoration and not print «~0 km», and only "" can do that
// — Go's template truth test runs on the STRING, where "0" is non-empty and
// lights its own guard. Swapping countOrEmpty for strconv.Itoa in that arm
// passes every other test in this file.
func TestRepairMatchingPostingDropsAZeroDistance(t *testing.T) {
	r := testRendererFull(t)
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_MATCHING_POSTING

	env := &notificationv1.NotificationEnvelope{
		Metadata: &notificationv1.EnvelopeMetadata{Type: nt},
		Payload: &notificationv1.NotificationEnvelope_SendRepairMatchingPosting{
			SendRepairMatchingPosting: &notificationv1.SendRepairMatchingPosting{
				Machinery: "Экскаватор Komatsu PC200", WorkTypes: "Гидравлика, Двигатель", DistanceKm: 0,
			}},
	}

	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("DEAD LETTER — ExtractParams: %v", err)
	}
	if got, ok := params["distance_km"]; !ok || got != "" {
		t.Fatalf(`distance_km = %q (present=%t), want "" — a zero must collapse the clause, and "0" would light it`, got, ok)
	}

	_, body, err := r.Render(nt, params, "en")
	if err != nil {
		t.Fatalf("DEAD LETTER — Render: %v", err)
	}
	if strings.Contains(body, "km") {
		t.Errorf("body still carries the distance clause with a zero distance: %q", body)
	}
	t.Logf("zero distance renders: %s", body)
}

// TestRepairBranchFlagsNeverAssertTheWrongEdition is the executable form of the
// rule flagWhen states for parts (its «USE IT IN PAIRS» doc comment in
// extract.go) and this family keeps in its string form: a discriminator lights
// ONE arm, and an absent or unrecognised value lights NEITHER.
//
// THE THIRD SUBTEST OF EACH GROUP IS THE POINT. The cheaper form —
// {{if eq .had_offer "true"}}…{{else}}…{{end}} — passes the first two cases and
// fails exactly the case the rule exists for: an empty had_offer would tell a
// customer whose request nobody answered that «the offer is no longer valid»,
// and an empty cancelled_by would name a side that did not cancel.
//
// COVERAGE, which the count assertion below pins: both arms of all four
// discriminators — `had_offer` and `deal_completed` (a true and a false edition
// each, plus the unknown), `cancelled_by` (its two named editions plus the
// unknown that must fall through to the bare actor), and `supersedes_previous`
// on BOTH types that read it, since its single {{if}} arm is written twice.
// Two further cases share the mechanism without being discriminator arms: an
// UNRECOGNISED had_offer (the absent case above proves the empty string, not a
// junk value), and Р26's distance clause PRESENT — see each case's own note.
func TestRepairBranchFlagsNeverAssertTheWrongEdition(t *testing.T) {
	r := testRendererFull(t)

	type editionCase struct {
		name     string
		nt       notificationv1.NotificationType
		params   map[string]string
		contains []string
		absent   []string
	}

	cases := []editionCase{
		{"expired_with_offer",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED,
			map[string]string{"machinery": "Экскаватор", "had_offer": "true", "provider_name": "Гидромаш"},
			[]string{"offer is no longer valid"}, []string{"never answered"}},
		{"expired_without_offer",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED,
			map[string]string{"machinery": "Экскаватор", "had_offer": "false", "provider_name": "Гидромаш"},
			[]string{"never answered"}, []string{"offer is no longer valid"}},
		// provider_name is present here ON PURPOSE, and it is what gives this
		// case teeth: both had_offer arms nest their sentence inside a second
		// {{if .provider_name}}. Drop the name and the unknown-flag body is
		// identical whether the outer guard is a two-armed {{else if}} or a
		// careless {{else}} — the case would pass against the very template it
		// exists to reject.
		{"expired_unknown_flag",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED,
			map[string]string{"machinery": "Экскаватор", "provider_name": "Гидромаш"},
			[]string{"request expired", "request bank"}, []string{"offer is no longer valid", "never answered"}},
		// An UNRECOGNISED value rather than an absent one. Both take the same
		// {{if eq}} path, so this case proves what the one above only implies —
		// and a producer shipping "maybe" (or "TRUE", or "1") is the realistic
		// shape of that bug. provider_name is present for the same reason it is
		// above: without it the body cannot tell a two-armed {{else if}} from a
		// careless {{else}}.
		{"expired_unrecognised_flag",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED,
			map[string]string{"machinery": "Экскаватор", "had_offer": "maybe", "provider_name": "Гидромаш"},
			[]string{"request expired", "request bank"}, []string{"offer is no longer valid", "never answered"}},
		{"review_deal_completed",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_RECEIVED,
			map[string]string{"customer_name": "Стройтранс", "machinery": "Экскаватор", "rating": "5", "deal_completed": "true"},
			[]string{"work was confirmed"}, []string{"did not take place"}},
		{"review_deal_cancelled",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_RECEIVED,
			map[string]string{"customer_name": "Стройтранс", "machinery": "Экскаватор", "rating": "3", "deal_completed": "false"},
			[]string{"did not take place"}, []string{"work was confirmed"}},
		{"review_unknown_flag",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_RECEIVED,
			map[string]string{"customer_name": "Стройтранс", "machinery": "Экскаватор", "rating": "3"},
			[]string{"left a review"}, []string{"work was confirmed", "did not take place"}},
		{"cancelled_by_customer",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED,
			map[string]string{"cancelled_by": "customer", "actor_name": "Стройтранс", "machinery": "Экскаватор", "reason": "Передумал"},
			[]string{"The customer Стройтранс"}, []string{"The service Стройтранс"}},
		{"cancelled_by_provider",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED,
			map[string]string{"cancelled_by": "provider", "actor_name": "Гидромаш", "machinery": "Экскаватор", "reason": "Нет запчастей"},
			[]string{"The service Гидромаш"}, []string{"The customer Гидромаш"}},
		{"cancelled_by_unknown",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED,
			map[string]string{"actor_name": "Гидромаш", "machinery": "Экскаватор", "reason": "Нет запчастей"},
			[]string{"Гидромаш cancelled"}, []string{"The customer", "The service"}},
		{"offer_supersedes",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_SENT,
			map[string]string{"provider_name": "Гидромаш", "machinery": "Экскаватор", "offer": "12 500 ₽", "supersedes_previous": "true"},
			[]string{"(updated offer)"}, nil},
		{"offer_first",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_SENT,
			map[string]string{"provider_name": "Гидромаш", "machinery": "Экскаватор", "offer": "12 500 ₽", "supersedes_previous": "false"},
			[]string{"12 500 ₽."}, []string{"(updated offer)"}},
		{"new_price_supersedes",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_PROPOSED,
			map[string]string{"provider_name": "Гидромаш", "machinery": "Экскаватор", "price": "18 000 ₽", "supersedes_previous": "true"},
			[]string{"replaces the previous proposal"}, nil},
		{"new_price_first",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_PROPOSED,
			map[string]string{"provider_name": "Гидромаш", "machinery": "Экскаватор", "price": "18 000 ₽"},
			[]string{"accept or decline"}, []string{"replaces the previous proposal"}},
		// Not a discriminator, same one-guard mechanism — and the only edition of
		// this family nothing else renders: the zero-distance lock pins "0" and
		// the degradation sweep pins the absent value, both by asserting the
		// clause is GONE. Without this case «, ~{{.distance_km}} km» could be
		// deleted from the baseline and every test in the package would pass.
		{"matching_posting_with_distance",
			notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_MATCHING_POSTING,
			map[string]string{"machinery": "Экскаватор", "work_types": "Гидравлика, Двигатель", "distance_km": "12"},
			[]string{"~12 km"}, nil},
	}

	// A deleted case is a discriminator arm that stops being watched, and
	// nothing else in the file would notice: the loop below simply runs one
	// subtest fewer and still reports ok.
	if len(cases) != 15 {
		t.Fatalf("the edition table has %d cases, want 15 — both arms of had_offer, deal_completed and cancelled_by plus their unknown, an unrecognised had_offer, supersedes_previous on both types that read it, and the distance clause present", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body, err := r.Render(tc.nt, tc.params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(body, want) {
					t.Errorf("body %q does not carry %q", body, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(body, bad) {
					t.Errorf("WRONG EDITION — body %q asserts %q", body, bad)
				}
			}
		})
	}
}

// TestRepairTemplatesDegradeCleanlyWithEveryOptionalAbsent renders every repair
// type with its REQUIRED params only — which is precisely the wire shape,
// because protojson omits an empty string and Render then fills the absent
// optional with "".
//
// It asserts the OUTPUT SHAPE rather than the wording: the failure it catches
// is a guard written on the wrong param or forgotten, and its signature is
// always a dangling fragment — «Заявка #.», a double space where a collapsed
// clause used to be, an empty pair of parentheses.
func TestRepairTemplatesDegradeCleanlyWithEveryOptionalAbsent(t *testing.T) {
	r := testRendererFull(t)

	artefacts := []string{": .", ": ,", " .", " ,", "  ", "«»", "''", "#.", "# ", "— .", "()"}

	covered := 0
	for nt, key := range typeKey {
		if !strings.HasPrefix(key, "repair_") {
			continue
		}
		covered++
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
			// Р26's distance is the one optional this family formats from a
			// NUMBER, and its collapse leaves a residue the generic list above
			// cannot name: the clause is «, ~{{.distance_km}} km», so a guard on
			// the wrong param prints «~ km» — punctuation-clean and meaningless.
			// TestRepairMatchingPostingDropsAZeroDistance pins the zero; this
			// pins the absent, which is the shape a legacy producer sends.
			if key == "repair_matching_posting" {
				for _, bad := range []string{"~", " km"} {
					if strings.Contains(body, bad) {
						t.Errorf("the distance clause survived an absent distance_km — body carries %q: %q", bad, body)
					}
				}
			}
			t.Logf("%s", body)
		})
	}
	if covered != 31 {
		t.Errorf("the catalog carries %d repair_ sections, want 31 (master §3.3, values 134–164)", covered)
	}
}
