package notifyrender

import (
	"slices"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// The three listing-moderation notices name the listing's vertical
// (OTDELRAZRABOTKI-958, owner's pick (b) of 10.11.2026). The vertical rides as
// the OPTIONAL discriminator `listing_service` — sale | rent | delivery |
// repair, the spelling of SendChatSectionClosed.object_service — and every
// value outside that vocabulary, the empty one and an absent key included,
// must render the text that shipped before the field, byte for byte: a
// producer on an older go-common sends nothing, and a notice it emits must
// neither fail nor change.

// listingNoticeCase is one of the three notices: its type, its section, the
// envelope its producer writes for a given vertical, and the exact English of
// its title and of the body before the field existed.
type listingNoticeCase struct {
	nt       notificationv1.NotificationType
	key      string
	required []string
	env      func(service string) *notificationv1.NotificationEnvelope
	title    string
	// body renders the body for a vertical word ("" = no vertical named).
	body func(word string) string
}

func listingNoticeCases() []listingNoticeCase {
	return []listingNoticeCase{
		{
			nt:       notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED,
			key:      "listing_approved",
			required: []string{"listing_title"},
			env: func(service string) *notificationv1.NotificationEnvelope {
				return stamped(notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED, &notificationv1.NotificationEnvelope{
					Payload: &notificationv1.NotificationEnvelope_SendListingApproved{
						SendListingApproved: &notificationv1.SendListingApproved{
							ListingId: "L1", ListingTitle: "CAT 320", ListingService: service,
						},
					},
				})
			},
			title: "Listing approved",
			body:  func(word string) string { return "Your " + word + "listing 'CAT 320' is now live." },
		},
		{
			nt:       notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED,
			key:      "listing_rejected",
			required: []string{"listing_title", "reason"},
			env: func(service string) *notificationv1.NotificationEnvelope {
				return stamped(notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED, &notificationv1.NotificationEnvelope{
					Payload: &notificationv1.NotificationEnvelope_SendListingRejected{
						SendListingRejected: &notificationv1.SendListingRejected{
							ListingId: "L1", ListingTitle: "CAT 320", Reason: "no photos", ListingService: service,
						},
					},
				})
			},
			title: "Listing rejected",
			body:  func(word string) string { return "Your " + word + "listing 'CAT 320' was rejected: no photos" },
		},
		{
			nt:       notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED,
			key:      "listing_unpublished",
			required: []string{"listing_title", "reason"},
			env: func(service string) *notificationv1.NotificationEnvelope {
				return stamped(notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED, &notificationv1.NotificationEnvelope{
					Payload: &notificationv1.NotificationEnvelope_SendListingUnpublished{
						SendListingUnpublished: &notificationv1.SendListingUnpublished{
							ListingId: "L1", ListingTitle: "CAT 320", Reason: "outdated", ListingService: service,
						},
					},
				})
			},
			title: "Listing unpublished",
			body:  func(word string) string { return "Your " + word + "listing 'CAT 320' was unpublished: outdated" },
		},
	}
}

// todaysListingBodies is the baseline every one of the three notices carried
// before OTDELRAZRABOTKI-958, verbatim. A value that names no vertical must
// render exactly these.
var todaysListingBodies = map[string]string{
	"listing_approved":    "Your listing 'CAT 320' is now live.",
	"listing_rejected":    "Your listing 'CAT 320' was rejected: no photos",
	"listing_unpublished": "Your listing 'CAT 320' was unpublished: outdated",
}

// TestListingNoticesDeclareListingServiceOptional pins the contract: the
// required tier is unchanged (a producer on an older go-common still
// publishes), and listing_service is the one optional param.
func TestListingNoticesDeclareListingServiceOptional(t *testing.T) {
	for _, tc := range listingNoticeCases() {
		if got := RequiredParams(tc.nt); !slices.Equal(got, tc.required) {
			t.Errorf("%s: required params = %v, want %v — listing_service must never be required", tc.key, got, tc.required)
		}
		if got := OptionalParams(tc.nt); !slices.Equal(got, []string{"listing_service"}) {
			t.Errorf("%s: optional params = %v, want [listing_service]", tc.key, got)
		}
		allowed, ok := AllowedParamsByKey(tc.key + ".body")
		if !ok || !contains(allowed, "listing_service") {
			t.Errorf("%s: AllowedParamsByKey does not admit listing_service (allowed=%v) — an i18n-catalog overlay reading it would be refused", tc.key, allowed)
		}
	}
}

// TestListingNoticesNameTheVertical is the copy lock: each of the four
// verticals gets its word, and every other shape — the empty value an unset
// field carries, a case-shifted token (the comparison is exact, like every
// other discriminator), and values from outside the vocabulary — renders
// today's text byte for byte. The title never changes.
func TestListingNoticesNameTheVertical(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	words := map[string]string{
		"sale":     "sale ",
		"rent":     "rental ",
		"delivery": "delivery ",
		"repair":   "repair ",
		// No vertical named: today's text.
		"":            "",
		"SALE":        "",
		"Rent":        "",
		"parts":       "",
		"marketplace": "",
	}

	for _, tc := range listingNoticeCases() {
		if got := tc.body(""); got != todaysListingBodies[tc.key] {
			t.Fatalf("%s: fixture drift — the no-vertical body %q is not today's %q", tc.key, got, todaysListingBodies[tc.key])
		}
		for service, word := range words {
			t.Run(tc.key+"/listing_service="+service, func(t *testing.T) {
				params, err := ExtractParams(tc.env(service))
				if err != nil {
					t.Fatalf("ExtractParams: %v", err)
				}
				if got, ok := params["listing_service"]; !ok || got != service {
					t.Fatalf("ExtractParams listing_service = %q (present=%v), want %q", got, ok, service)
				}
				title, body, err := r.Render(tc.nt, params, "en")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if title != tc.title {
					t.Errorf("title = %q, want %q", title, tc.title)
				}
				if want := tc.body(word); body != want {
					t.Errorf("body =\n  %q\nwant\n  %q", body, want)
				}
			})
		}
	}
}

// TestListingNoticesRenderWithTheParamAbsent is the legacy-caller case: a
// params map with no listing_service key at all, which is what a caller that
// builds the map by hand without the field passes. The resolver runs
// missingkey=error and text/template's eq would also refuse a missing value,
// so this renders only because Render fills a declared optional with "".
func TestListingNoticesRenderWithTheParamAbsent(t *testing.T) {
	r := newTestRenderer(t, map[string]string{})
	for _, tc := range listingNoticeCases() {
		t.Run(tc.key, func(t *testing.T) {
			params, err := ExtractParams(tc.env("rent"))
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			delete(params, "listing_service")
			for _, loc := range []string{"en", "ru"} {
				title, body, err := r.Render(tc.nt, params, loc)
				if err != nil {
					t.Fatalf("Render(%s) with listing_service absent: %v — the optional fill must stop missingkey=error and eq on a missing key", loc, err)
				}
				if title != tc.title || body != todaysListingBodies[tc.key] {
					t.Errorf("%s: absent key rendered (%q, %q), want today's (%q, %q)", loc, title, body, tc.title, todaysListingBodies[tc.key])
				}
			}
		})
	}
}

// TestListingNoticesAcceptAnOverlayReadingTheVertical stands in for the
// i18n-catalog PR that follows this one: an overlay that branches on
// listing_service is loaded (not refused as an undeclared placeholder) and
// renders both with the vertical and with the key absent. An overlay still on
// today's text — the shipped en.json — keeps rendering too.
func TestListingNoticesAcceptAnOverlayReadingTheVertical(t *testing.T) {
	r := newTestRenderer(t, map[string]string{
		"en.json": `{
  "listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."}
}`,
		"ru.json": `{
  "listing_approved": {"title": "Объявление одобрено", "body": "Ваше объявление{{if eq .listing_service \"rent\"}} аренды{{end}} «{{.listing_title}}» опубликовано."}
}`,
	})
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED

	for _, tc := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"rent", map[string]string{"listing_title": "CAT 320", "listing_service": "rent"}, "Ваше объявление аренды «CAT 320» опубликовано."},
		{"absent", map[string]string{"listing_title": "CAT 320"}, "Ваше объявление «CAT 320» опубликовано."},
	} {
		_, body, err := r.Render(nt, tc.params, "ru")
		if err != nil {
			t.Fatalf("ru/%s: Render: %v", tc.name, err)
		}
		if body != tc.want {
			t.Errorf("ru/%s: body = %q, want %q", tc.name, body, tc.want)
		}
	}

	// The shipped en overlay predates the field and shadows the baseline: it
	// still renders, with or without the param.
	for _, params := range []map[string]string{
		{"listing_title": "CAT 320", "listing_service": "sale"},
		{"listing_title": "CAT 320"},
	} {
		_, body, err := r.Render(nt, params, "en")
		if err != nil {
			t.Fatalf("en: Render: %v", err)
		}
		if body != "Your listing 'CAT 320' is now live." {
			t.Errorf("en overlay body = %q", body)
		}
	}
}
