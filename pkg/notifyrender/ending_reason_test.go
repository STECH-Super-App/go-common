package notifyrender

import (
	"sort"
	"strings"
	"testing"
	"text/template"
	"text/template/parse"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ── Every person-ended deal names its reason (TESTIROVANIE-254) ──────────────
//
// Owner decision of 16.10.2026: every cancel or reject notification, in every
// vertical, names the reason — and it must STAY true as verticals and cancel
// doors are added («can we ensure that cross-module?»). The fleet's answer has
// three parts: this guard (the catalog side), i18n-catalog's checker (the
// translations) and the e2e suite (the producers). This file is the first.
//
// The guard has two halves:
//
//   - TestEveryEndingNotificationIsClassified walks the proto enum and finds
//     every NotificationType whose NAME says something ended (endingMarkers).
//     Each one must be classified, exactly once: either in personEndedTypes,
//     which this file then holds to the decision, or in
//     endingTypesWithoutAReason with a kind and a one-line why. A new vertical's
//     cancel type therefore cannot ship unclassified: it is red here the day
//     gen-go-lib carries it.
//
//   - TestPersonEndedNotificationsNameTheirReason holds every classified type
//     to the decision end to end, through the same public path a consumer uses:
//     the payload FIELD reaches the param in ExtractParams, the catalog declares
//     the param, and the English baseline actually puts the reason into the
//     sentence — a LABEL printed as given, or a CODE with one arm per code of
//     its closed vocabulary.
//
// What it cannot see: a person-ended type whose name carries no marker. The
// marker list is deliberately wide (it includes EXPIRED and a few words no type
// uses yet), and the classification lists are the reviewable record.

// endingMarkers are the enum-name SEGMENTS that say a notice reports something
// ending. Matched as whole «_»-separated segments, so a future «…_DISCLOSED»
// is not a «CLOSED». CANCELED is the US spelling, listed so a misspelt new type
// cannot slip past; EXPIRED is listed although no person ever expires anything,
// because a timer ending is a decision worth writing down too; TERMINATED,
// ABORTED, REFUSED and DENIED name no type today and are here for the next
// vertical's vocabulary.
var endingMarkers = []string{
	"CANCELLED", "CANCELED", "REJECTED", "DECLINED", "WITHDRAWN", "CLOSED",
	"EXPIRED", "TERMINATED", "ABORTED", "REFUSED", "DENIED",
}

// reasonShape is how a template may carry a reason.
type reasonShape int

const (
	// reasonLabel: a free text — a dictionary label snapshot, or the actor's
	// stated reason — printed as given, a literal {{.param}} in the baseline.
	reasonLabel reasonShape = iota + 1
	// reasonCode: a token from a closed vocabulary, never printed. The baseline
	// carries one {{if eq .param "CODE"}} arm per code, worded as the code's
	// label, and NO default arm: an unknown code must light nothing rather than
	// assert a reason the wire did not say.
	reasonCode
)

// reasonParam is one reason a person-ended type carries. The param name is also
// the payload's proto FIELD name — every reason on the wire today follows that
// convention, and the guard relies on it to prove the field reaches the param.
type reasonParam struct {
	name  string
	shape reasonShape
	// codes is the WHOLE closed vocabulary of a reasonCode param, in the
	// producer's display order. Every code needs its own arm.
	codes []string
}

// The closed vocabularies, spelled exactly as directives.proto documents them
// on each field and as order-service accepts them (case-sensitive).
var (
	// SendOrderCancelled.cancel_reason — А-16 as amended 06.10.2026: the
	// renter's list, then the lessor's (OTHER closes both).
	rentCancelCodes = []string{
		"CHANGED_PLANS", "FOUND_ANOTHER", "WRONG_DETAILS",
		"MACHINE_UNAVAILABLE", "MACHINE_FAULTY", "TERMS_NOT_SUITABLE", "CUSTOMER_UNREACHABLE",
		"OTHER",
	}
	// SendOrderCancelled.decline_reason — the renter's counter-offer decline.
	rentDeclineCodes = []string{"PRICE", "DATES", "TERMS", "FOUND_ANOTHER", "OTHER"}
	// SendDeliveryRequestRejected.reject_reason — Д-18, the carrier's reject.
	deliveryRejectCodes = []string{"NOT_ON_ROUTE", "MACHINE_NOT_SUITABLE", "NO_FREE_MACHINE", "OTHER"}
	// SendDeliveryRequestCancelled.cancel_reason — Д-18: the customer's list,
	// the carrier's list, OTHER (shared by both), and the late cancel's one
	// code (order-service internal/domain/delivery/cancel.go).
	deliveryCancelCodes = []string{
		"CHANGED_PLANS", "FOUND_ANOTHER_CARRIER", "WRONG_DETAILS",
		"MACHINE_UNAVAILABLE", "MACHINE_FAULTY", "CUSTOMER_UNREACHABLE",
		"OTHER", "DELIVERY_PROBLEM",
	}
)

// personEndedTypes is every notification in which a PERSON ends a deal or a
// request, with the reason it must name.
var personEndedTypes = map[notificationv1.NotificationType][]reasonParam{
	// Rent: one type for every human cancel. decline_reason is the renter's
	// counter-offer decline, cancel_reason any other human cancel; the producer
	// never sets both.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED: {
		{name: "cancel_reason", shape: reasonCode, codes: rentCancelCodes},
		{name: "decline_reason", shape: reasonCode, codes: rentDeclineCodes},
	},
	// Delivery: the carrier's reject and either side's cancel (Д-18).
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_REJECTED: {
		{name: "reject_reason", shape: reasonCode, codes: deliveryRejectCodes},
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED: {
		{name: "cancel_reason", shape: reasonCode, codes: deliveryCancelCodes},
	},
	// Repair: the D25 dictionary's label snapshot on the direct request, and the
	// customer's cancel reason on an open posting.
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_REJECTED:  {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_CANCELLED: {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_CANCELLED: {{name: "reason", shape: reasonLabel}},
	// Parts: the actor's stated reason, every one of them a label.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_REJECTED_BY_SELLER:   {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_BUYER:   {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_SELLER:  {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_ADMIN:   {{name: "reason", shape: reasonLabel}},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CANCELLED: {{name: "reason", shape: reasonLabel}},
}

// exclusionKind is WHY an ending notice owes no reason. Four kinds, and a new
// exclusion must fit one of them — a fifth is a question for the owner, not a
// line in this file.
type exclusionKind string

const (
	// The door that fires it takes no reason, or the type itself IS the reason
	// («the linked rental was cancelled», «the shop closed»).
	noReasonDoor exclusionKind = "no-reason door"
	// Nobody decided it: a timer, a sweep, or a consequence of another event.
	systemEnding exclusionKind = "system or timer ending"
	// Only an offer inside the deal ended; the deal itself goes on.
	dealContinues exclusionKind = "deal continues"
	// Not a marketplace deal or request at all: moderation, team membership,
	// an organisation's admin handoff, the price-list matching queue.
	notADeal exclusionKind = "not a deal or request"
)

type exclusion struct {
	kind exclusionKind
	why  string
}

// endingTypesWithoutAReason is every enum value with an ending marker in its
// name that owes NO reason under the 16.10.2026 decision, each with its reason.
var endingTypesWithoutAReason = map[notificationv1.NotificationType]exclusion{
	// ── no-reason doors ──
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED:                 {noReasonDoor, "the type is the reason: the linked rental was cancelled"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_WITHDRAWN:                  {noReasonDoor, "a seller withdrawing a bank response states no reason"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_RESPONSE_DECLINED:                   {noReasonDoor, "the type is the reason: the customer chose another contractor"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_BUYER:     {noReasonDoor, "the buyer's «close» on a sourcing request takes no reason (unlike its cancel)"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_ALL_SHOPS_DECLINED:          {noReasonDoor, "the type is the reason: every shop declined"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_BY_SHOP:     {noReasonDoor, "a shop withdrawing its quote states no reason"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_SHOP_CLOSED: {noReasonDoor, "the type is the reason: the shop closed"},
	// ── system and timer endings ──
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_CANCELLED:                         {systemEnding, "the 14-day inactivity sweep"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED:                     {systemEnding, "Д-30: the loading date passed"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_BUYER:                    {systemEnding, "М-25: the seller's 72 h response window ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_SELLER:                   {systemEnding, "М-25: the seller's 72 h response window ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_BUYER: {systemEnding, "closed automatically because an order was placed from it"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_SHOP:  {systemEnding, "closed automatically because the buyer placed an order"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_BUYER:         {systemEnding, "the sourcing request's shelf life ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_SHOP:          {systemEnding, "the sourcing request's shelf life ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED:                       {systemEnding, "Р47: the direct request's response window ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_EXPIRED_PROVIDER:              {systemEnding, "Р47: the direct request's response window ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REQUEST_AUTO_CANCELLED:                {systemEnding, "D23: the idle-cancel sweep out of CONFIRMED"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_CUSTOMER:              {systemEnding, "the open posting's shelf life ran out"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_EXPIRED_RESPONDER:             {systemEnding, "the open posting's shelf life ran out"},
	// ── the deal continues ──
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_WITHDRAWN:    {dealContinues, "the owner withdrew a counter-offer; the rental request stands"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_DECLINED:  {dealContinues, "the request returns to its original terms"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_WITHDRAWN: {dealContinues, "the request returns to its original terms"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_DECLINED:            {dealContinues, "the service may propose other terms"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_OFFER_WITHDRAWN:           {dealContinues, "the offer ended, the direct request stands"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_DECLINED:        {dealContinues, "a mid-job price ended; the previous agreement stands"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_NEW_PRICE_WITHDRAWN:       {dealContinues, "a mid-job price ended; the previous agreement stands"},
	// ── not a deal or request ──
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED:                   {notADeal, "listing moderation (it prints the moderator's reason anyway)"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_REJECTED:              {notADeal, "organisation verification (it prints the reason anyway)"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_REJECTED:       {notADeal, "an organisation change request's moderation (it prints the reasons anyway)"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_REJECTED: {notADeal, "Р42 catalogue moderation (it prints the reason anyway)"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_REJECTED:           {notADeal, "Р40's matching queue counts rejected price-list rows"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED:                    {notADeal, "a team invitation"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_REJECTED:        {notADeal, "an organisation's admin-role handoff"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_CANCELLED:       {notADeal, "an organisation's admin-role handoff"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_EXPIRED:         {notADeal, "an organisation's admin-role handoff, ended by its timer"},
}

// endingMarkerOf returns the first ending marker that is a whole segment of an
// enum name, or "".
func endingMarkerOf(enumName string) string {
	segments := strings.Split(strings.TrimPrefix(enumName, "NOTIFICATION_TYPE_"), "_")
	for _, seg := range segments {
		for _, marker := range endingMarkers {
			if seg == marker {
				return marker
			}
		}
	}
	return ""
}

func TestEveryEndingNotificationIsClassified(t *testing.T) {
	names := make([]string, 0, len(notificationv1.NotificationType_value))
	for name := range notificationv1.NotificationType_value {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		nt := notificationv1.NotificationType(notificationv1.NotificationType_value[name])
		marker := endingMarkerOf(name)
		_, covered := personEndedTypes[nt]
		excl, excluded := endingTypesWithoutAReason[nt]

		switch {
		case covered && excluded:
			t.Errorf("%s is in BOTH personEndedTypes and endingTypesWithoutAReason (%s: %s) — it either owes a reason or it does not; keep one entry", name, excl.kind, excl.why)
		case marker != "" && !covered && !excluded:
			t.Errorf("%s reports an ending (its name carries %q) and nothing here says whether it names its reason.\n"+
				"  Owner decision of 16.10.2026 (TESTIROVANIE-254): every notice in which a PERSON ends a deal or request names the reason.\n"+
				"  Either (a) a person ends it: carry the reason — a field on its proto payload named like the param, its ExtractParams line,\n"+
				"      the param in the catalog (OPTIONAL while any producer cannot fill it), and an English baseline that prints a label\n"+
				"      ({{.reason}}) or has one {{if eq}} arm per code — then add it to personEndedTypes in this file;\n"+
				"  or (b) it owes none: add it to endingTypesWithoutAReason with its kind (%q, %q, %q or %q) and a one-line why.\n"+
				"  A door a PERSON fires belongs in (a); put it in (b) only on the owner's word.",
				name, marker, noReasonDoor, systemEnding, dealContinues, notADeal)
		case marker == "" && excluded:
			t.Errorf("%s is listed in endingTypesWithoutAReason (%s) but its name carries no ending marker — the entry excuses nothing; remove it", name, excl.kind)
		}
	}

	for nt, excl := range endingTypesWithoutAReason {
		switch excl.kind {
		case noReasonDoor, systemEnding, dealContinues, notADeal:
		default:
			t.Errorf("%v is excluded with an unknown kind %q — use one of the four, or take a fifth to the owner", nt, excl.kind)
		}
		if strings.TrimSpace(excl.why) == "" {
			t.Errorf("%v is excluded with no why — the reviewer reads this list, say why it owes no reason", nt)
		}
	}
}

func TestPersonEndedNotificationsNameTheirReason(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only: the guard is about the English source of truth

	for nt, reasons := range personEndedTypes {
		key, mapped := typeKey[nt]
		t.Run(nt.String(), func(t *testing.T) {
			if !mapped {
				t.Fatalf("%v is a person-ended type with no catalog entry — it cannot render at all", nt)
			}
			if len(reasons) == 0 {
				t.Fatalf("%v is listed with no reason param — list the param(s) it carries", nt)
			}
			for _, reason := range reasons {
				t.Run(reason.name, func(t *testing.T) {
					assertReasonDeclared(t, nt, reason)
					assertReasonExtracted(t, nt, reason)
					switch reason.shape {
					case reasonLabel:
						assertLabelPrinted(t, r, nt, key, reasons, reason)
					case reasonCode:
						assertEveryCodeHasAnArm(t, r, nt, reasons, reason)
					default:
						t.Fatalf("%v.%s has no shape — reasonLabel (printed) or reasonCode (one arm per code)", nt, reason.name)
					}
				})
			}
		})
	}
}

// assertReasonDeclared: the catalog must declare the param, or Render's
// absent-optional fill skips it and i18n-catalog's overlay validator refuses a
// translation that reads it. A CODE is never printed, so it can only be
// optional — ValidateBundleComplete fails a required param with no literal
// {{.name}} placeholder.
func assertReasonDeclared(t *testing.T, nt notificationv1.NotificationType, reason reasonParam) {
	t.Helper()
	if !contains(allowedParams(nt), reason.name) {
		t.Errorf("%v does not declare %q in requiredParams or optionalParams (catalog.go) — the reason cannot reach a template. Declare it OPTIONAL unless every producer always fills it", nt, reason.name)
	}
	if reason.shape == reasonCode && contains(RequiredParams(nt), reason.name) {
		t.Errorf("%v declares the code %q REQUIRED — a code is read only inside {{if eq}} and never printed, so it must be optional (a required param needs a literal placeholder)", nt, reason.name)
	}
}

// assertReasonExtracted proves the payload FIELD reaches the param: it sets the
// field by name on the type's payload arm and reads ExtractParams back. A field
// on the wire with no ExtractParams line renders as nothing, silently.
func assertReasonExtracted(t *testing.T, nt notificationv1.NotificationType, reason reasonParam) {
	t.Helper()
	const probe = "PROBE_REASON_254"

	env, ok := envelopeWithField(t, nt, reason.name, probe)
	if !ok {
		return
	}
	params, err := ExtractParams(env)
	if err != nil {
		t.Errorf("ExtractParams(%v): %v", nt, err)
		return
	}
	if got := params[reason.name]; got != probe {
		t.Errorf("%v: the payload field %q carries %q but ExtractParams emits %q for it — add the field to the type's ExtractParams arm", nt, reason.name, probe, got)
	}
}

// envelopeWithField builds an envelope of type nt whose payload has field set
// to value. The payload arm is found by the fleet's naming convention —
// NOTIFICATION_TYPE_FOO_BAR is carried by SendFooBar — and the field by the
// reason param's name.
func envelopeWithField(t *testing.T, nt notificationv1.NotificationType, field, value string) (*notificationv1.NotificationEnvelope, bool) {
	t.Helper()
	want := payloadNameFor(nt)
	oneof := (&notificationv1.NotificationEnvelope{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	for i := 0; i < oneof.Fields().Len(); i++ {
		fd := oneof.Fields().Get(i)
		if string(fd.Message().Name()) != want {
			continue
		}
		env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: nt}}
		payload := env.ProtoReflect().Mutable(fd).Message()
		f := payload.Descriptor().Fields().ByName(protoreflect.Name(field))
		if f == nil || f.Kind() != protoreflect.StringKind || f.IsList() {
			t.Errorf("%s has no singular string field %q — the reason param must be carried by the payload field of the same name", want, field)
			return nil, false
		}
		payload.Set(f, protoreflect.ValueOfString(value))
		return env, true
	}
	t.Errorf("no envelope payload arm is named %s — the guard finds a type's payload as Send<CamelCase type name>; if %v breaks that convention, teach envelopeWithField the exception", want, nt)
	return nil, false
}

// payloadNameFor maps NOTIFICATION_TYPE_PARTS_ORDER_REJECTED_BY_SELLER to
// SendPartsOrderRejectedBySeller.
func payloadNameFor(nt notificationv1.NotificationType) string {
	var b strings.Builder
	b.WriteString("Send")
	for _, word := range strings.Split(strings.TrimPrefix(nt.String(), "NOTIFICATION_TYPE_"), "_") {
		if word == "" {
			continue
		}
		b.WriteString(word[:1])
		b.WriteString(strings.ToLower(word[1:]))
	}
	return b.String()
}

// paramsFor fills every required param and sets the type's reasons: `reason`
// to value, every OTHER reason of the type to "" — the producer sets one at a
// time (rent's decline_reason and cancel_reason are never both set).
func paramsFor(nt notificationv1.NotificationType, all []reasonParam, reason reasonParam, value string) map[string]string {
	params := validParamsFor(nt)
	for _, other := range all {
		params[other.name] = ""
	}
	params[reason.name] = value
	return params
}

// assertLabelPrinted: a LABEL reason is a literal {{.param}} placeholder in the
// English baseline, and the value the producer sent is in the rendered text.
func assertLabelPrinted(t *testing.T, r *Renderer, nt notificationv1.NotificationType, key string, all []reasonParam, reason reasonParam) {
	t.Helper()
	used := append(extractPlaceholders(BaselineEN[key+".title"]), extractPlaceholders(BaselineEN[key+".body"])...)
	if !contains(used, reason.name) {
		t.Errorf("baseline %s never prints {{.%s}} — a person-ended notice must name its reason (TESTIROVANIE-254)", key, reason.name)
	}
	const label = "Plans changed 254"
	title, body, err := r.Render(nt, paramsFor(nt, all, reason, label), "en")
	if err != nil {
		t.Fatalf("Render(%v) with %s=%q: %v", nt, reason.name, label, err)
	}
	if !strings.Contains(title+" "+body, label) {
		t.Errorf("%v rendered without its reason %q: title=%q body=%q", nt, label, title, body)
	}
}

// assertEveryCodeHasAnArm: each code of the closed vocabulary changes the
// rendered text, never shows up raw, and differs from every other code; and no
// {{if eq .param …}} chain ends in a bare {{else}} — a default arm would assert
// a reason the wire did not say for an unknown code, and for an EMPTY one too.
func assertEveryCodeHasAnArm(t *testing.T, r *Renderer, nt notificationv1.NotificationType, all []reasonParam, reason reasonParam) {
	t.Helper()
	if len(reason.codes) == 0 {
		t.Fatalf("%v.%s is a reasonCode with no vocabulary — list every code the producer may send", nt, reason.name)
	}
	key := typeKey[nt]
	for _, part := range []string{key + ".title", key + ".body"} {
		if chainEndsInDefault(t, BaselineEN[part], reason.name) {
			t.Errorf("baseline %s: an {{if eq .%s …}} chain ends in a bare {{else}} — that arm then speaks for an empty or unknown code too; give every code its own arm and drop the {{else}}", part, reason.name)
		}
	}
	render := func(code string) string {
		t.Helper()
		title, body, err := r.Render(nt, paramsFor(nt, all, reason, code), "en")
		if err != nil {
			t.Fatalf("Render(%v) with %s=%q: %v", nt, reason.name, code, err)
		}
		return title + "\n" + body
	}

	none := render("")
	if unknown := render("NOT_A_CODE_254"); unknown != none {
		t.Errorf("%v: an unknown %s renders %q, but no code renders %q — the baseline has a default arm; give every code its own {{if eq}} arm and no {{else}}", nt, reason.name, unknown, none)
	}
	seen := map[string]string{}
	for _, code := range reason.codes {
		got := render(code)
		switch {
		case got == none:
			t.Errorf("%v: %s=%q renders the same text as no reason at all (%q) — add an {{if eq .%s %q}} arm to the English baseline, worded as the code's label", nt, reason.name, code, none, reason.name, code)
		case strings.Contains(got, code):
			t.Errorf("%v: %s=%q prints the raw code: %q — a code selects a sentence, it is never shown", nt, reason.name, code, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%v: %s=%q and %q render the same text %q — each code needs its own wording", nt, reason.name, code, prev, got)
		}
		seen[got] = code
	}
}

// chainEndsInDefault reports whether tmpl holds an {{if eq .param "…"}} chain —
// the head and every {{else if eq .param "…"}} after it — whose last link has a
// bare {{else}}. text/template parses {{else if b}} as an {{else}} holding one
// nested {{if b}}, so the chain is followed through ElseList.
func chainEndsInDefault(t *testing.T, tmpl, param string) bool {
	t.Helper()
	parsed, err := template.New(param).Parse(tmpl)
	if err != nil {
		t.Fatalf("baseline does not parse: %v", err)
	}
	found := false
	walkIfNodes(parsed.Root, func(n *parse.IfNode) {
		if !isEqOnField(n.Pipe, param) {
			return
		}
		for cur := n; cur.ElseList != nil; {
			nodes := cur.ElseList.Nodes
			next, isIf := nodes[0].(*parse.IfNode)
			if len(nodes) != 1 || !isIf || !isEqOnField(next.Pipe, param) {
				found = true
				return
			}
			cur = next
		}
	})
	return found
}

// walkIfNodes calls visit on every {{if}} in the tree, nested ones included.
func walkIfNodes(node parse.Node, visit func(*parse.IfNode)) {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkIfNodes(child, visit)
		}
	case *parse.IfNode:
		visit(n)
		walkIfNodes(n.List, visit)
		walkIfNodes(n.ElseList, visit)
	case *parse.RangeNode:
		walkIfNodes(n.List, visit)
		walkIfNodes(n.ElseList, visit)
	case *parse.WithNode:
		walkIfNodes(n.List, visit)
		walkIfNodes(n.ElseList, visit)
	}
}

// isEqOnField reports whether pipe is exactly `eq .field "literal"`.
func isEqOnField(pipe *parse.PipeNode, field string) bool {
	if pipe == nil || len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 3 {
		return false
	}
	args := pipe.Cmds[0].Args
	fn, isIdent := args[0].(*parse.IdentifierNode)
	f, isField := args[1].(*parse.FieldNode)
	_, isString := args[2].(*parse.StringNode)
	return isIdent && fn.Ident == "eq" && isField && len(f.Ident) == 1 && f.Ident[0] == field && isString
}

// TestChainEndsInDefault keeps the structural check honest on planted
// templates, since the shipped baselines only ever show it the passing shape.
func TestChainEndsInDefault(t *testing.T) {
	for tmpl, want := range map[string]bool{
		`A.{{if eq .r "X"}} x.{{else if eq .r "Y"}} y.{{end}}`:                  false,
		`A.{{if eq .r "X"}} x.{{else if eq .r "Y"}} y.{{else}} other.{{end}}`:   true,
		`A.{{if eq .r "X"}} x.{{else}} other.{{end}}`:                           true,
		`{{if eq .side "c"}}C{{else}}N{{end}}.{{if eq .r "X"}} x.{{end}}`:       false, // another param's default is not this one's
		`{{if .n}}{{if eq .r "X"}} x.{{else}} other.{{end}}{{end}}`:             true,  // nested inside another branch
		`A.{{if eq .r "X"}} x.{{else if eq .other "Y"}} y.{{end}}`:              true,  // the chain hands over to another param: X's else speaks for every r ≠ X
		`A.{{if eq .r "X"}} x.{{else if eq .r "Y"}} y.{{else}}{{.n}}{{end}} B.`: true,
	} {
		if got := chainEndsInDefault(t, tmpl, "r"); got != want {
			t.Errorf("chainEndsInDefault(%q) = %v, want %v", tmpl, got, want)
		}
	}
}

// ── Copy locks: the English texts TESTIROVANIE-254 wrote ─────────────────────
//
// The guard above proves every code has SOME arm; these pin the exact English,
// which i18n-catalog mirrors in Russian.

// TestRenderDeliveryRequestRejected_ReasonArms pins the carrier's Д-18 reject
// clause. No reason, an unknown code, a lower-cased code and a code from
// another list all render the sentence that shipped before the field existed.
func TestRenderDeliveryRequestRejected_ReasonArms(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_REJECTED
	const sentence = "The carrier declined your request."

	clauses := map[string]string{
		"NOT_ON_ROUTE":         " Reason: not on the carrier's route.",
		"MACHINE_NOT_SUITABLE": " Reason: the equipment is not suitable.",
		"NO_FREE_MACHINE":      " Reason: no equipment is free on this date.",
		"OTHER":                " Reason: another reason — see the request card.",
		"":                     "",
		absentKey:              "",
		"BOGUS":                "",
		"not_on_route":         "", // case-sensitive, like the order-service list
		"CHANGED_PLANS":        "", // a cancel code is not a reject code
	}
	for code, clause := range clauses {
		t.Run(strings.ReplaceAll("reason="+code, absentKey, "absent"), func(t *testing.T) {
			title, body, err := r.Render(nt, armParams(map[string]string{"reject_reason": code}), "en")
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if title != "Request declined" {
				t.Errorf("title = %q, want %q", title, "Request declined")
			}
			if want := sentence + clause; body != want {
				t.Errorf("body = %q, want %q", body, want)
			}
			for _, tok := range []string{"NOT_ON_ROUTE", "MACHINE_NOT_SUITABLE", "NO_FREE_MACHINE", "BOGUS", "not_on_route", "CHANGED_PLANS"} {
				if strings.Contains(body, tok) {
					t.Errorf("body %q prints the raw token %q", body, tok)
				}
			}
		})
	}
}

// TestRenderDeliveryRequestCancelled_ReasonArms pins the cancelling side's
// Д-18 clause under every side arm. The reason APPENDS to the side sentence
// TestRenderDeliveryRequestCancelled_SideArms locks, so the matrix is the
// product of the two; no reason, an unknown code, a reject code and rent's
// FOUND_ANOTHER all drop the clause.
func TestRenderDeliveryRequestCancelled_ReasonArms(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED

	sides := map[string]string{
		"customer": "The customer cancelled request #12.",
		"carrier":  "The carrier cancelled request #12.",
		"":         "Request #12 was cancelled.",
		absentKey:  "Request #12 was cancelled.",
	}
	clauses := map[string]string{
		"CHANGED_PLANS":         " Reason: plans changed.",
		"FOUND_ANOTHER_CARRIER": " Reason: another carrier was found.",
		"WRONG_DETAILS":         " Reason: the request details were wrong.",
		"MACHINE_UNAVAILABLE":   " Reason: the equipment became unavailable.",
		"MACHINE_FAULTY":        " Reason: the equipment is faulty.",
		"CUSTOMER_UNREACHABLE":  " Reason: the customer cannot be reached.",
		"DELIVERY_PROBLEM":      " Reason: a problem with the delivery.",
		"OTHER":                 " Reason: another reason — see the request card.",
		"":                      "",
		absentKey:               "",
		"BOGUS":                 "",
		"changed_plans":         "", // case-sensitive, like the order-service lists
		"NOT_ON_ROUTE":          "", // a reject code is not a cancel code
		"FOUND_ANOTHER":         "", // rent's code, not delivery's
	}
	for side, sentence := range sides {
		for code, clause := range clauses {
			name := strings.ReplaceAll("side="+side+"/reason="+code, absentKey, "absent")
			t.Run(name, func(t *testing.T) {
				_, body, err := r.Render(nt, armParams(map[string]string{
					"cancelled_by": side, "cancel_reason": code, "request_no": "12",
				}), "en")
				if err != nil {
					t.Fatalf("Render err: %v", err)
				}
				if want := sentence + clause; body != want {
					t.Errorf("body = %q, want %q", body, want)
				}
				for _, tok := range []string{"CHANGED_PLANS", "FOUND_ANOTHER", "WRONG_DETAILS", "MACHINE_", "CUSTOMER_UNREACHABLE", "DELIVERY_PROBLEM", "BOGUS", "NOT_ON_ROUTE"} {
					if strings.Contains(body, tok) {
						t.Errorf("body %q prints the raw token %q", body, tok)
					}
				}
			})
		}
	}
}

// TestRenderRepairPostingCancelled_Reason pins the responder's notice of a
// cancelled open posting: the customer's reason label leads the body, printed
// as given, and without one the body is the text that shipped before the field
// existed — the sale-service producer could not fill it when it was minted.
func TestRenderRepairPostingCancelled_Reason(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_POSTING_CANCELLED
	const today = "Your response to the Экскаватор Komatsu PC200 request is closed as well. Other open requests are waiting in the request bank."

	for reason, want := range map[string]string{
		"Изменились планы": "Reason: Изменились планы. " + today,
		"":                 today,
		absentKey:          today,
	} {
		t.Run(strings.ReplaceAll("reason="+reason, absentKey, "absent"), func(t *testing.T) {
			title, body, err := r.Render(nt, armParams(map[string]string{
				"machinery": "Экскаватор Komatsu PC200", "reason": reason,
			}), "en")
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if title != "The customer cancelled the request" {
				t.Errorf("title = %q", title)
			}
			if body != want {
				t.Errorf("body = %q, want %q", body, want)
			}
		})
	}
}
