package notifyrender

import (
	"maps"
	"slices"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// The carrier LISTING's В-41(в) pair (173, 174, OTDELRAZRABOTKI-731): «the
// company's verification hid your listing» and «it came back». sale-service's
// Delivery module writes them from its organisation.events fold, in the
// transaction that moves the carrier's one listing (Д-01) into or out of
// «Заблокировано», tenant-addressed to the carrier's Владелец and Менеджеры
// (owner decision of 23.10.2026, parity with parts' Р51 pair 124/125).
//
// They get their own file because neither belongs to a family the other files
// test: they are not order-service's delivery REQUEST family (47–64) and not
// review-service's moderation pair (168–169), yet they share the DELIVERY_
// prefix with both — which is why the delivery census at the bottom walks the
// enum by that prefix.
//
// Each payload carries one field, the status the block interrupted (the
// restore returns the listing to exactly that status, В-41(а)), in delivery's
// lower-case listing vocabulary: moderation | active | rejected | paused. Only
// "active" may render the vault's catalogue sentence; every other value, the
// empty one included, renders the neutral edition — a carrier whose listing was
// never in the catalogue is never told it left or came back (Р56·В-53's
// principle).

// deliveryListingBlockCase is one notice of the pair: its type and section, the
// one param its payload carries, the envelope its producer writes for a given
// status, and the exact English of its title and of both body editions.
type deliveryListingBlockCase struct {
	nt    notificationv1.NotificationType
	key   string
	param string
	env   func(status string) *notificationv1.NotificationEnvelope
	title string
	// active is the vault's sentence («Уведомления перевозки» :65-66), owed only
	// when the status is exactly "active".
	active string
	// neutral is the edition every other value renders; it claims no catalogue
	// change, so it is true under every status the wire can carry.
	neutral string
}

func deliveryListingBlockCases() []deliveryListingBlockCase {
	return []deliveryListingBlockCase{
		{
			nt:    notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LISTING_BLOCKED,
			key:   "delivery_listing_blocked",
			param: "interrupted_status",
			env: func(status string) *notificationv1.NotificationEnvelope {
				return stamped(notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LISTING_BLOCKED, &notificationv1.NotificationEnvelope{
					Payload: &notificationv1.NotificationEnvelope_SendDeliveryListingBlocked{
						SendDeliveryListingBlocked: &notificationv1.SendDeliveryListingBlocked{InterruptedStatus: status},
					},
				})
			},
			title:   "Delivery listing hidden",
			active:  "Your company's verification is suspended — your listing is temporarily hidden from the catalogue. It will come back automatically once verification is restored.",
			neutral: "Your company's verification is suspended — your listing is blocked. Once verification is restored, it returns to its previous status.",
		},
		{
			nt:    notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LISTING_RESTORED,
			key:   "delivery_listing_restored",
			param: "restored_status",
			env: func(status string) *notificationv1.NotificationEnvelope {
				return stamped(notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LISTING_RESTORED, &notificationv1.NotificationEnvelope{
					Payload: &notificationv1.NotificationEnvelope_SendDeliveryListingRestored{
						SendDeliveryListingRestored: &notificationv1.SendDeliveryListingRestored{RestoredStatus: status},
					},
				})
			},
			title:   "Delivery listing restored",
			active:  "Verification restored — your listing is published in the catalogue again.",
			neutral: "Verification restored — the block is lifted and your listing is back in its previous status.",
		},
	}
}

// TestDeliveryListingBlockPairRendersRatherThanDeadLetters is the question
// «does a 173/174 directive render, or does it dead-letter?» asked of both
// types: the catalog entry, both baseline halves, the param contract, the
// envelope arm the send_<type> rule names, and a render in both locales of the
// shapes a producer can put on the wire — the status it fills and the empty
// message a producer that leaves it unset sends.
func TestDeliveryListingBlockPairRendersRatherThanDeadLetters(t *testing.T) {
	r := testRendererFull(t)

	for _, tc := range deliveryListingBlockCases() {
		t.Run(tc.key, func(t *testing.T) {
			if got := typeKey[tc.nt]; got != tc.key {
				t.Fatalf("typeKey = %q, want %q — an unmapped type dead-letters at inbox-service on first delivery", got, tc.key)
			}
			for _, half := range []string{".title", ".body"} {
				if BaselineEN[tc.key+half] == "" {
					t.Errorf("baseline has no %s%s", tc.key, half)
				}
			}
			if got := RequiredParams(tc.nt); len(got) != 0 {
				t.Errorf("required params = %v, want none — the status is a discriminator and is never printed", got)
			}
			if got := OptionalParams(tc.nt); !slices.Equal(got, []string{tc.param}) {
				t.Errorf("optional params = %v, want exactly [%s]", got, tc.param)
			}

			// The arm, found by the naming rule notification-service's dead-letter
			// diagnosis derives it by — and it must be the arm the producer sets.
			fd := payloadFieldFor(tc.nt)
			if fd == nil {
				t.Fatalf("no envelope arm named by the send_<type> rule")
			}
			if set := tc.env("active").ProtoReflect().WhichOneof(fd.ContainingOneof()); set != fd {
				t.Fatalf("the send_<type> rule names arm %s, but the producer's payload sets %v", fd.Name(), set)
			}
			bare := &notificationv1.NotificationEnvelope{Metadata: &notificationv1.EnvelopeMetadata{Type: tc.nt}}
			bare.ProtoReflect().Mutable(fd)

			for shape, env := range map[string]*notificationv1.NotificationEnvelope{
				"empty payload": bare,
				"active":        tc.env("active"),
				"moderation":    tc.env("moderation"),
			} {
				params, err := ExtractParams(env)
				if err != nil {
					t.Fatalf("%s: DEAD LETTER — ExtractParams: %v", shape, err)
				}
				for _, loc := range []string{"en", "ru"} {
					title, body, err := r.Render(tc.nt, params, loc)
					if err != nil {
						t.Fatalf("%s: DEAD LETTER — Render(%s): %v", shape, loc, err)
					}
					if title == "" || body == "" {
						t.Errorf("%s/%s: empty title/body (title=%q body=%q)", shape, loc, title, body)
					}
				}
			}
		})
	}
}

// TestDeliveryListingBlockPairPicksTheEditionByStatus is the copy lock for both
// editions, and the rule that only the wire's own "active" may assert a
// catalogue change. The table is every status a block can interrupt
// (sale-service blockableStatuses()), the empty value an unset field carries,
// and three that must never light the catalogue arm: a case-shifted «ACTIVE»
// (the comparison is exact, like every other discriminator), and the two
// statuses the block and the archive write, which never reach this field. A key
// left out of the map entirely renders the neutral edition too, through
// Render's absent-optional fill.
func TestDeliveryListingBlockPairPicksTheEditionByStatus(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	statuses := []string{"active", "moderation", "rejected", "paused", "", "ACTIVE", "blocked", "archived"}

	for _, tc := range deliveryListingBlockCases() {
		if tc.active == tc.neutral {
			t.Fatalf("%s: the two editions are the same sentence", tc.key)
		}

		for _, status := range statuses {
			t.Run(tc.key+"/status="+status, func(t *testing.T) {
				params, err := ExtractParams(tc.env(status))
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}
				title, body, err := r.Render(tc.nt, params, "en")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				want := tc.neutral
				if status == "active" {
					want = tc.active
				}
				if title != tc.title {
					t.Errorf("title = %q, want %q (one title for both editions)", title, tc.title)
				}
				if body != want {
					t.Errorf("body =\n  %q\nwant\n  %q", body, want)
				}
				if status != "active" && strings.Contains(body, "catalogue") {
					t.Errorf("status %q renders a catalogue change the listing never made: %q", status, body)
				}
				for _, tok := range []string{"active", "ACTIVE", "moderation", "rejected", "paused", "archived"} {
					if strings.Contains(body, tok) {
						t.Errorf("body %q prints the raw status token %q", body, tok)
					}
				}
			})
		}

		t.Run(tc.key+"/status=absent", func(t *testing.T) {
			title, body, err := r.Render(tc.nt, map[string]string{}, "en")
			if err != nil {
				t.Fatalf("Render with the key absent: %v — the optional fill must stop missingkey=error", err)
			}
			if title != tc.title || body != tc.neutral {
				t.Errorf("rendered (%q, %q), want (%q, %q)", title, body, tc.title, tc.neutral)
			}
		})
	}
}

// TestDeliveryListingBlockPairExtractsTheRawToken pins the extract arms: each
// surfaces its one field under the field's own name, verbatim — no case
// folding, no mapping onto a flag — and an empty message surfaces "" rather
// than no key, because inbox-service renders whatever this map holds.
func TestDeliveryListingBlockPairExtractsTheRawToken(t *testing.T) {
	for _, tc := range deliveryListingBlockCases() {
		for _, status := range []string{"paused", "ACTIVE", ""} {
			t.Run(tc.key+"/status="+status, func(t *testing.T) {
				params, err := ExtractParams(tc.env(status))
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}
				if want := map[string]string{tc.param: status}; !maps.Equal(params, want) {
					t.Errorf("params = %v, want exactly %v", params, want)
				}
			})
		}
	}
}

// TestEveryDeliveryTypeIsCatalogued is the census the delivery family lacked —
// parts and repair have theirs (TestPartsCatalogCoversEveryDeclaredType,
// TestRepairCatalogCoversEveryDeclaredType). It walks the proto enum rather
// than a hand-written list, so a DELIVERY_ type minted in proto-contracts is
// asked for its catalog entry, both baseline halves and its ExtractParams arm
// the day gen-go-lib carries it, rather than on notification.events.dlq.inbox.
func TestEveryDeliveryTypeIsCatalogued(t *testing.T) {
	declared := 0
	for value, name := range notificationv1.NotificationType_name {
		if !strings.HasPrefix(name, "NOTIFICATION_TYPE_DELIVERY_") {
			continue
		}
		declared++
		nt := notificationv1.NotificationType(value)

		key, mapped := typeKey[nt]
		if !mapped {
			t.Errorf("%s has NO catalog entry — every directive of this type dead-letters at inbox-service on first delivery", name)
			continue
		}
		for _, half := range []string{".title", ".body"} {
			if BaselineEN[key+half] == "" {
				t.Errorf("%s (%q): baseline has no %s", name, key, half)
			}
		}

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

	// 22 = order-service's request family 47–64 (18), review-service's
	// moderation pair 168–169, and sale-service's carrier listing pair 173–174.
	if declared != 22 {
		t.Errorf("proto declares %d NOTIFICATION_TYPE_DELIVERY_* values, expected 22 (47–64, 168, 169, 173, 174) — a type was added or removed, and this test is the place that has to notice", declared)
	}
}
