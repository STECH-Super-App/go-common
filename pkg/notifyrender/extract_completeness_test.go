package notifyrender

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"google.golang.org/protobuf/reflect/protoreflect"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// ExtractParams is a hand-written type switch over the envelope's payload oneof,
// and until this file nothing asked it whether it reads everything the wire
// carries. The two failure modes it could not see are the ones that cost a
// message rather than a build:
//
//   - a payload arm nobody wrote — every directive of that type dead-letters on
//     first delivery (inbox-service wraps the error raw), which is exactly the
//     parts incident of 08.2026; and
//   - a FIELD added to an existing payload with no line added to its arm — the
//     value simply never reaches the template, and every existing test still
//     passes because it asserts the params it already knows about.
//
// The second one is live right now: proto-contracts is adding `posting_no` to
// the four customer-addressed repair board payloads and `deal_completed` to
// SendRepairReviewWindowEnding (repair critique NOTIF-04 / NOTIF-06). Neither
// field exists at this module's gen-go-lib pin, so neither can be read yet —
// and when the pin moves, this test is what turns the missing arms into a red
// line instead of a field that quietly renders as nothing.
//
// SCOPE: singular and repeated STRING fields. Ids are exempt as a family rule
// (below) and the one numeric param, distance_km, is read and needs no rule.

// idSuffix is the documented category exemption. NO TEMPLATE READS AN ID: an id
// travels in deep_link.params, which inbox-service persists separately from the
// rendered text, and a uuid in a push is a bug in every locale. The rule is
// stated across the catalog (the repair family's header comment says it in so
// many words), so the ~80 `*_id` fields are exempt by rule rather than by an
// enumeration nobody could review.
const idSuffix = "_id"

// notTemplated is every remaining declared string field that ExtractParams
// deliberately does not surface under its own name, with the reason. An entry
// here is a claim about intent, and the test checks BOTH directions: a field
// that stops existing, or that starts being extracted, fails until the entry is
// removed — so this list cannot rot into a silencer.
//
// Three kinds of entry, and only the third is «unused»:
//   - consumed into a derived param: a discriminator the template reads as a
//     boolean through flagWhen, never printed;
//   - addressed elsewhere: a recipient address read by notification-service's
//     own SMS/email path, not by a template;
//   - read by nothing: on the wire, empty by contract or simply never phrased.
var notTemplated = map[string]string{
	"SendDeliveryRequestCancelled.cancel_reason":            "optional, late-cancellation only, and not templated — the text names cancelled_by and request_no",
	"SendInviteSms.phone":                                   "the SMS recipient address; notification-service's SMS path reads it, the template does not",
	"SendMemberRoleChanged.new_role":                        "a routing discriminator — the producer picks the TYPE from it (member_role_changed_manager vs _operator); the text names only the tenant",
	"SendPartsFavoritePriceDropped.currency":                "no parts arm reads currency (DECISIONS §30.2); the «₽» in the baseline is a rendering, not data",
	"SendPartsOfferBackInStock.currency":                    "no parts arm reads currency (DECISIONS §30.2)",
	"SendPartsOrderCreated.currency":                        "no parts arm reads currency (DECISIONS §30.2)",
	"SendPartsSourcingQuoteReceived.currency":               "no parts arm reads currency (DECISIONS §30.2)",
	"SendPartsSubscriptionOfferAppeared.currency":           "no parts arm reads currency (DECISIONS §30.2)",
	"SendPartsOrderConfirmed.fulfilment_kind":               "consumed into is_pickup / is_carrier via flagWhen — a discriminator, never printed",
	"SendPartsOrderConfirmed.deadline_basis":                "consumed into is_from_payment via flagWhen",
	"SendPartsOrderConfirmedPartially.partial_kind":         "consumed into the partial-confirmation flags via flagWhen",
	"SendPartsOrderFulfilmentOverdueBuyer.fulfilment_kind":  "consumed into the pickup/carrier flags via flagWhen",
	"SendPartsOrderFulfilmentOverdueSeller.fulfilment_kind": "consumed into the pickup/carrier flags via flagWhen",
	"SendPartsReviewComplaintResolved.outcome":              "consumed into outcome_hidden / outcome_no_violation via flagWhen",
	"SendPartsOrderContactHandover.tenant_name":             "on the wire, empty by contract: C31 bars a tenant name from a directive, so 123 renders its COLLAPSED form («вашей команды»)",
	"SendPartsOrderContactHandover.from_user_name":          "on the wire, empty by contract — same C31 collapse as tenant_name",
	"SendWalletOperationDecided.reason":                     "read by nothing: the text names amount, currency and decision",
}

// unmappedByDesign is every payload arm ExtractParams deliberately does not
// claim. They are the SMS/email-only payloads that go through
// notification-service's own template path, plus chat's push-only arm — none of
// them renders through this catalog. Adding an arm here is how a real gap would
// be hidden, so the test also fails when an entry starts resolving.
var unmappedByDesign = map[string]string{
	"SendAccountDeletionOtpSms":  "SMS-only; notification-service's legacy template path renders it",
	"SendContactPhoneOtpSms":     "SMS-only; sale-service's contact-phone OTP, rendered by notification-service",
	"SendOrgAdminTransferOtpSms": "SMS-only; the admin-transfer confirmation code",
	"SendChatMessageReceived":    "chat's own push payload; it carries no catalog type",
}

func TestExtractParamsReadsEveryDeclaredStringField(t *testing.T) {
	oneof := (&notificationv1.NotificationEnvelope{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	if oneof == nil {
		t.Fatal("NotificationEnvelope has no `payload` oneof — the walk below is built on it")
	}

	seenArms := map[string]bool{}
	usedExemptions := map[string]bool{}

	for i := 0; i < oneof.Fields().Len(); i++ {
		fd := oneof.Fields().Get(i)
		msg := string(fd.Message().Name())
		seenArms[msg] = true

		env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{}}
		env.ProtoReflect().Mutable(fd)

		params, err := ExtractParams(env)
		if err != nil {
			if _, ok := unmappedByDesign[msg]; !ok {
				t.Errorf("%s has NO ExtractParams arm — every directive carrying it dead-letters on first delivery; add the arm, or record it in unmappedByDesign with the reason", msg)
				continue
			}
			var appErr *commonerr.AppError
			if !errors.As(err, &appErr) || appErr.Reason != ReasonUnknownType {
				t.Errorf("%s: unmapped-by-design arms must fail with %s, got %v", msg, ReasonUnknownType, err)
			}
			continue
		}
		if reason, ok := unmappedByDesign[msg]; ok {
			t.Errorf("%s now HAS an ExtractParams arm but is still listed unmappedByDesign (%q) — drop the entry", msg, reason)
		}

		fields := fd.Message().Fields()
		for j := 0; j < fields.Len(); j++ {
			f := fields.Get(j)
			if f.Kind() != protoreflect.StringKind {
				continue
			}
			name := string(f.Name())
			key := msg + "." + name
			_, extracted := params[name]
			exempt := strings.HasSuffix(name, idSuffix)
			reason, listed := notTemplated[key]
			if listed {
				usedExemptions[key] = true
			}
			switch {
			case extracted && listed:
				t.Errorf("%s is extracted now but is still listed notTemplated (%q) — drop the entry", key, reason)
			case !extracted && !exempt && !listed:
				t.Errorf("%s is declared on the wire and reaches no param — a producer that fills it renders nothing, silently. Extract it, or record it in notTemplated with the reason", key)
			}
		}
	}

	for key := range notTemplated {
		if !usedExemptions[key] {
			t.Errorf("notTemplated has a stale entry %q — the field no longer exists (or its payload is no longer an envelope arm)", key)
		}
	}
	for msg := range unmappedByDesign {
		if !seenArms[msg] {
			t.Errorf("unmappedByDesign has a stale entry %q — that payload is no longer an envelope arm", msg)
		}
	}
}

// TestExtractParamsCompletenessCensus prints nothing and asserts the shape of
// the walk itself: the arm count is the number this file was written against,
// so a new payload arm is noticed even if it happens to need no exemption. It
// is a census in the sense the repair and parts liveproof tests use the word —
// the guard against a silent «nobody added it to the list» — and the number is
// meant to be updated in the same commit that adds the arm.
func TestExtractParamsCompletenessCensus(t *testing.T) {
	oneof := (&notificationv1.NotificationEnvelope{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	const want = 175 // 175: SendOrderReviewInvite (З-08, 06.10.2026)
	if got := oneof.Fields().Len(); got != want {
		names := make([]string, 0, oneof.Fields().Len())
		for i := 0; i < oneof.Fields().Len(); i++ {
			names = append(names, string(oneof.Fields().Get(i).Message().Name()))
		}
		sort.Strings(names)
		t.Errorf("the envelope declares %d payload arms, this file was written against %d — %s", got, want, fmt.Sprintf("arms: %s", strings.Join(names, ", ")))
	}
}
