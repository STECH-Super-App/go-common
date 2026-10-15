package notifyrender

import (
	"maps"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// The review-moderation family (166–171): review-service's hide and
// complaint-ruling notices for the three verticals that had none — rent
// (ORDER_, by the family convention 165 follows), delivery and repair. Parts
// keeps its own 120 and 131, and these are their twins: the same two shapes,
// the same flags, and the resolved text copied from 131 byte for byte.
//
// review-service is the only producer, and every directive here is
// PERSON-addressed (the review's author, the complaint's filer). That is why
// this family gets its own file rather than a row in the rent, delivery or
// repair tests: none of those producers emits it.

// reviewModerationReason is the moderator's free-text reason as the hidden
// notices carry it — verbatim, never resolved from a key.
const reviewModerationReason = "Оскорбления в тексте отзыва"

// reviewModerationReviewID is the review the directives point at. It rides the
// deep link and is never read by a template.
const reviewModerationReviewID = "01920070-0001-7000-8000-000000700001"

// The distinctive phrase of each resolved edition, as the en baseline renders
// it — the same two phrases parts' 131 uses.
const (
	resolvedHiddenPhrase      = "the review is hidden"
	resolvedNoViolationPhrase = "no violation was found"
)

// reviewHiddenCase is one hidden notice: its type, the envelope its producer
// writes, and the exact English body the en baseline must render for it.
type reviewHiddenCase struct {
	nt   notificationv1.NotificationType
	key  string
	env  func(reason string) *notificationv1.NotificationEnvelope
	body string
}

func reviewHiddenCases() []reviewHiddenCase {
	return []reviewHiddenCase{
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_HIDDEN,
			key: "order_review_hidden",
			env: func(reason string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendOrderReviewHidden{
					SendOrderReviewHidden: &notificationv1.SendOrderReviewHidden{ReviewId: reviewModerationReviewID, Reason: reason},
				}}
			},
			body: "Your review of the lessor was hidden by a moderator. Reason: " + reviewModerationReason + ".",
		},
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_HIDDEN,
			key: "delivery_review_hidden",
			env: func(reason string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendDeliveryReviewHidden{
					SendDeliveryReviewHidden: &notificationv1.SendDeliveryReviewHidden{ReviewId: reviewModerationReviewID, Reason: reason},
				}}
			},
			body: "Your review of the carrier was hidden by a moderator. Reason: " + reviewModerationReason + ".",
		},
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_HIDDEN,
			key: "repair_review_hidden",
			env: func(reason string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendRepairReviewHidden{
					SendRepairReviewHidden: &notificationv1.SendRepairReviewHidden{ReviewId: reviewModerationReviewID, Reason: reason},
				}}
			},
			body: "Your repair review was hidden by a moderator. Reason: " + reviewModerationReason + ".",
		},
	}
}

// reviewResolvedCase is one resolved notice: its type and the envelope its
// producer writes for a given outcome.
type reviewResolvedCase struct {
	nt  notificationv1.NotificationType
	key string
	env func(outcome string) *notificationv1.NotificationEnvelope
}

func reviewResolvedCases() []reviewResolvedCase {
	return []reviewResolvedCase{
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_COMPLAINT_RESOLVED,
			key: "order_review_complaint_resolved",
			env: func(outcome string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendOrderReviewComplaintResolved{
					SendOrderReviewComplaintResolved: &notificationv1.SendOrderReviewComplaintResolved{ReviewId: reviewModerationReviewID, Outcome: outcome},
				}}
			},
		},
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_COMPLAINT_RESOLVED,
			key: "delivery_review_complaint_resolved",
			env: func(outcome string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendDeliveryReviewComplaintResolved{
					SendDeliveryReviewComplaintResolved: &notificationv1.SendDeliveryReviewComplaintResolved{ReviewId: reviewModerationReviewID, Outcome: outcome},
				}}
			},
		},
		{
			nt:  notificationv1.NotificationType_NOTIFICATION_TYPE_REPAIR_REVIEW_COMPLAINT_RESOLVED,
			key: "repair_review_complaint_resolved",
			env: func(outcome string) *notificationv1.NotificationEnvelope {
				return &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendRepairReviewComplaintResolved{
					SendRepairReviewComplaintResolved: &notificationv1.SendRepairReviewComplaintResolved{ReviewId: reviewModerationReviewID, Outcome: outcome},
				}}
			},
		},
	}
}

// stamped puts the metadata type on an envelope, so a failure names the type
// the way inbox-service's dead letter would.
func stamped(nt notificationv1.NotificationType, env *notificationv1.NotificationEnvelope) *notificationv1.NotificationEnvelope {
	env.Metadata = &notificationv1.EnvelopeMetadata{Type: nt}
	return env
}

// TestReviewModerationHiddenNoticesRender pins the three hidden notices end to
// end: the catalog entry, both baseline halves, the exact params map (the
// moderator's reason, and NOT the review id), and the rendered sentence. The
// reason is REQUIRED — a hide is refused without one — so it must be a literal
// placeholder, never a guarded clause.
func TestReviewModerationHiddenNoticesRender(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay

	for _, tc := range reviewHiddenCases() {
		t.Run(tc.key, func(t *testing.T) {
			if got := typeKey[tc.nt]; got != tc.key {
				t.Fatalf("typeKey = %q, want %q", got, tc.key)
			}
			if title := BaselineEN[tc.key+".title"]; title != "Review hidden" {
				t.Errorf("title baseline = %q, want %q", title, "Review hidden")
			}
			if _, ok := BaselineEN[tc.key+".body"]; !ok {
				t.Fatalf("baseline has no %s.body", tc.key)
			}
			if got := RequiredParams(tc.nt); len(got) != 1 || got[0] != "reason" {
				t.Errorf("required params = %v, want exactly [reason]", got)
			}
			if got := OptionalParams(tc.nt); len(got) != 0 {
				t.Errorf("optional params = %v, want none", got)
			}

			params, err := ExtractParams(stamped(tc.nt, tc.env(reviewModerationReason)))
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			if want := map[string]string{"reason": reviewModerationReason}; !maps.Equal(params, want) {
				t.Errorf("params = %v, want exactly %v", params, want)
			}

			title, body, err := r.Render(tc.nt, params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if title != "Review hidden" || body != tc.body {
				t.Errorf("rendered (%q, %q), want (%q, %q)", title, body, "Review hidden", tc.body)
			}
		})
	}
}

// TestReviewModerationResolvedNoticesNeverAssertTheWrongRuling is 131's rule
// for the three twins: HIDDEN lights only «the review is hidden»,
// NO_VIOLATION only «no violation was found», and an absent or unknown outcome
// lights NEITHER — the sentence then says only that the complaint was reviewed,
// and leaves no dangling fragment behind. A complaint with no recorded ruling
// must never read as resolved in either party's favour.
//
// The texts are 131's, byte for byte: each body is asserted equal to what
// parts_review_complaint_resolved renders for the same outcome.
func TestReviewModerationResolvedNoticesNeverAssertTheWrongRuling(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	const parts = notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED

	outcomes := []struct {
		name    string
		outcome string
		params  map[string]string
		body    string
	}{
		{
			name: "hidden", outcome: "HIDDEN",
			params: map[string]string{"outcome_hidden": "1", "outcome_no_violation": ""},
			body:   "The complaint about the review was reviewed: the review is hidden.",
		},
		{
			name: "no violation", outcome: "NO_VIOLATION",
			params: map[string]string{"outcome_hidden": "", "outcome_no_violation": "1"},
			body:   "The complaint about the review was reviewed: no violation was found — the review stays.",
		},
		{
			name: "absent outcome", outcome: "",
			params: map[string]string{"outcome_hidden": "", "outcome_no_violation": ""},
			body:   "The complaint about the review was reviewed.",
		},
		{
			name: "unknown outcome", outcome: "WITHDRAWN",
			params: map[string]string{"outcome_hidden": "", "outcome_no_violation": ""},
			body:   "The complaint about the review was reviewed.",
		},
	}

	for _, tc := range reviewResolvedCases() {
		if got := typeKey[tc.nt]; got != tc.key {
			t.Errorf("%s: typeKey = %q, want %q", tc.nt, got, tc.key)
			continue
		}
		if BaselineEN[tc.key+".title"] != BaselineEN["parts_review_complaint_resolved.title"] ||
			BaselineEN[tc.key+".body"] != BaselineEN["parts_review_complaint_resolved.body"] {
			t.Errorf("%s: baseline is not parts_review_complaint_resolved's byte for byte", tc.key)
		}
		if got := RequiredParams(tc.nt); len(got) != 0 {
			t.Errorf("%s: required params = %v, want none — the outcome is two optional flags", tc.key, got)
		}

		for _, o := range outcomes {
			t.Run(tc.key+"/"+o.name, func(t *testing.T) {
				params, err := ExtractParams(stamped(tc.nt, tc.env(o.outcome)))
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}
				if !maps.Equal(params, o.params) {
					t.Errorf("params = %v, want exactly %v", params, o.params)
				}

				title, body, err := r.Render(tc.nt, params, "en")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if title != "Complaint reviewed" || body != o.body {
					t.Errorf("rendered (%q, %q), want (%q, %q)", title, body, "Complaint reviewed", o.body)
				}
				hidden := strings.Contains(body, resolvedHiddenPhrase)
				clean := strings.Contains(body, resolvedNoViolationPhrase)
				if hidden && clean {
					t.Errorf("both rulings asserted at once: %q", body)
				}
				for _, bad := range []string{": .", " .", "  ", "— ."} {
					if strings.Contains(body, bad) {
						t.Errorf("dangling %q in %q", bad, body)
					}
				}

				_, partsBody, err := r.Render(parts, params, "en")
				if err != nil {
					t.Fatalf("Render(131): %v", err)
				}
				if body != partsBody {
					t.Errorf("the twin drifted from 131\n  got  %q\n  131  %q", body, partsBody)
				}
			})
		}
	}
}

// TestReviewModerationFamilyIsComplete is the census for the family, and it
// walks the PROTO ENUM rather than the two hand-written case lists above: every
// `*_REVIEW_HIDDEN` / `*_REVIEW_COMPLAINT_RESOLVED` type outside parts (which
// keeps its own 120 and 131) must be exactly the six this file knows, each
// mapped, each with both baseline halves, and each with an ExtractParams arm
// that claims its payload. A seventh such type minted in proto-contracts — or a
// dropped one — fails here the moment gen-go-lib carries it, rather than on
// notification.events.dlq.inbox.
func TestReviewModerationFamilyIsComplete(t *testing.T) {
	known := map[notificationv1.NotificationType]bool{}
	for _, tc := range reviewHiddenCases() {
		known[tc.nt] = true
	}
	for _, tc := range reviewResolvedCases() {
		known[tc.nt] = true
	}
	if len(known) != 6 {
		t.Fatalf("the case lists name %d distinct types, want 6 (166–171)", len(known))
	}

	declared := map[notificationv1.NotificationType]bool{}
	for value, name := range notificationv1.NotificationType_name {
		moderation := strings.HasSuffix(name, "_REVIEW_HIDDEN") || strings.HasSuffix(name, "_REVIEW_COMPLAINT_RESOLVED")
		if !moderation || strings.HasPrefix(name, "NOTIFICATION_TYPE_PARTS_") {
			continue
		}
		nt := notificationv1.NotificationType(value)
		declared[nt] = true
		if !known[nt] {
			t.Errorf("%s is a review-moderation type this file does not know — add its case, its typeKey, its baseline and its ExtractParams arm", name)
		}

		key, ok := typeKey[nt]
		if !ok {
			t.Errorf("%s has no catalog entry — every directive of this type dead-letters at inbox-service", name)
			continue
		}
		for _, half := range []string{".title", ".body"} {
			if BaselineEN[key+half] == "" {
				t.Errorf("%s: baseline has no %s%s", name, key, half)
			}
		}

		// The arm, found through the payload the send_<type> rule names — so a
		// type nobody wrote a case for above is still asked for one.
		fd := payloadFieldFor(nt)
		if fd == nil {
			t.Errorf("%s has no envelope arm named by the send_<type> rule", name)
			continue
		}
		env := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: nt}}
		env.ProtoReflect().Mutable(fd)
		if _, err := ExtractParams(env); err != nil {
			t.Errorf("%s: ExtractParams: %v", name, err)
		}
	}

	for nt := range known {
		if !declared[nt] {
			t.Errorf("%s is in this file's case lists but the proto no longer declares it under the family's names", nt)
		}
	}
	if len(declared) != 6 {
		t.Errorf("the proto declares %d review-moderation types outside parts, want exactly 6 (166–171)", len(declared))
	}
}
