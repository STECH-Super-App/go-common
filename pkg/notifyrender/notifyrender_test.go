package notifyrender

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// validParamsFor builds a complete params map for the given type so
// Render's required-param check passes.
func validParamsFor(t notificationv1.NotificationType) map[string]string {
	out := make(map[string]string)
	for _, p := range requiredParams[t] {
		out[p] = "test_" + p
	}
	return out
}

// assertReason unwraps an AppError and checks its Reason matches want.
func assertReason(t *testing.T, err error, want string) {
	t.Helper()
	var appErr *commonerr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T: %v", err, err)
	}
	if appErr.Reason != want {
		t.Errorf("Reason = %q, want %q", appErr.Reason, want)
	}
}

func TestRenderEveryTypeEveryLocale(t *testing.T) {
	locales := []string{"en", "ru", "kk"}
	for nt := range typeKey {
		nt := nt
		for _, loc := range locales {
			loc := loc
			t.Run(nt.String()+"_"+loc, func(t *testing.T) {
				title, body, err := Render(nt, validParamsFor(nt), loc)
				if err != nil {
					t.Fatalf("Render returned err: %v", err)
				}
				if title == "" {
					t.Errorf("empty title for %s/%s", nt, loc)
				}
				if body == "" {
					t.Errorf("empty body for %s/%s", nt, loc)
				}
			})
		}
	}
}

func TestRenderMissingParam(t *testing.T) {
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED
	params := map[string]string{"listing_title": "X"} // missing "reason"
	_, _, err := Render(nt, params, "en")
	assertReason(t, err, ReasonMissingParam)
	var appErr *commonerr.AppError
	if errors.As(err, &appErr) {
		if appErr.Params["param"] != "reason" {
			t.Errorf("params[param] = %v, want 'reason'", appErr.Params["param"])
		}
	}
}

func TestRenderUnknownType(t *testing.T) {
	_, _, err := Render(notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED,
		map[string]string{}, "en")
	assertReason(t, err, ReasonUnknownType)

	_, _, err = Render(notificationv1.NotificationType_NOTIFICATION_TYPE_SYSTEM,
		map[string]string{}, "en")
	assertReason(t, err, ReasonUnknownType)
}

// TestExtractParams_SendListingUnpublished locks the ExtractParams mapping for
// the SendListingUnpublished payload: it must return exactly {listing_title,
// reason} and not fall through to the default ErrEmptyPayload branch.
func TestExtractParams_SendListingUnpublished(t *testing.T) {
	env := &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendListingUnpublished{
			SendListingUnpublished: &notificationv1.SendListingUnpublished{
				ListingId:    "listing-123",
				ListingTitle: "Excavator",
				Reason:       "missing docs",
			},
		},
	}

	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("ExtractParams returned err: %v", err)
	}

	want := map[string]string{
		"listing_title": "Excavator",
		"reason":        "missing docs",
	}
	if len(params) != len(want) {
		t.Fatalf("params = %v, want %v", params, want)
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("params[%q] = %q, want %q", k, params[k], v)
		}
	}
}

// TestExtractParams_SendListingUnpublishedRenders proves the full extract →
// render path for the new type lands non-empty title/body in every locale.
func TestExtractParams_SendListingUnpublishedRenders(t *testing.T) {
	env := &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendListingUnpublished{
			SendListingUnpublished: &notificationv1.SendListingUnpublished{
				ListingTitle: "Excavator",
				Reason:       "missing docs",
			},
		},
	}
	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("ExtractParams returned err: %v", err)
	}
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED
	for _, loc := range []string{"en", "ru", "kk"} {
		title, body, err := Render(nt, params, loc)
		if err != nil {
			t.Fatalf("Render(%s) err: %v", loc, err)
		}
		if title == "" || body == "" {
			t.Errorf("%s: empty title/body (title=%q body=%q)", loc, title, body)
		}
	}
}

// TestExtractAndRender_Slice2And4 locks the extract → render path for every
// net-new IN_APP type added in slices 2 (tenant/team lifecycle) and 4 (member
// lifecycle): each payload must extract exactly the expected params and render
// non-empty title/body in all three locales.
func TestExtractAndRender_Slice2And4(t *testing.T) {
	cases := []struct {
		name string
		nt   notificationv1.NotificationType
		env  *notificationv1.NotificationEnvelope
		want map[string]string
	}{
		{
			name: "tenant_rejected",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_REJECTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendTenantRejected{
					SendTenantRejected: &notificationv1.SendTenantRejected{
						TenantId: "t-1", Reason: "missing docs",
					},
				},
			},
			want: map[string]string{"reason": "missing docs"},
		},
		{
			name: "invite_accepted",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_ACCEPTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendInviteAccepted{
					SendInviteAccepted: &notificationv1.SendInviteAccepted{
						Phone: "+77001234567", Role: "manager",
					},
				},
			},
			want: map[string]string{"phone": "+77001234567", "role": "manager"},
		},
		{
			name: "invite_declined",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendInviteDeclined{
					SendInviteDeclined: &notificationv1.SendInviteDeclined{Phone: "+77001234567"},
				},
			},
			want: map[string]string{"phone": "+77001234567"},
		},
		{
			name: "admin_transferred_new",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_NEW,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferredNew{
					SendAdminTransferredNew: &notificationv1.SendAdminTransferredNew{TeamName: "Crew A"},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "admin_transferred_old",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_OLD,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferredOld{
					SendAdminTransferredOld: &notificationv1.SendAdminTransferredOld{TeamName: "Crew A"},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "member_blocked",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendMemberBlocked{
					SendMemberBlocked: &notificationv1.SendMemberBlocked{TeamId: "tm-1", TeamName: "Crew A"},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "member_unblocked",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendMemberUnblocked{
					SendMemberUnblocked: &notificationv1.SendMemberUnblocked{TeamId: "tm-1", TeamName: "Crew A"},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "member_removed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendMemberRemoved{
					SendMemberRemoved: &notificationv1.SendMemberRemoved{TeamId: "tm-1", TeamName: "Crew A"},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "team_member_removed_admin",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendTeamMemberRemovedAdmin{
					SendTeamMemberRemovedAdmin: &notificationv1.SendTeamMemberRemovedAdmin{
						TeamId: "tm-1", TeamName: "Crew A", RemovedMemberName: "Ivan",
					},
				},
			},
			want: map[string]string{"team_name": "Crew A", "removed_member_name": "Ivan"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("ExtractParams err: %v", err)
			}
			if len(params) != len(tc.want) {
				t.Fatalf("params = %v, want %v", params, tc.want)
			}
			for k, v := range tc.want {
				if params[k] != v {
					t.Errorf("params[%q] = %q, want %q", k, params[k], v)
				}
			}
			for _, loc := range []string{"en", "ru", "kk"} {
				title, body, err := Render(tc.nt, params, loc)
				if err != nil {
					t.Fatalf("Render(%s) err: %v", loc, err)
				}
				if title == "" || body == "" {
					t.Errorf("%s/%s: empty title/body (title=%q body=%q)", tc.name, loc, title, body)
				}
			}
		})
	}
}

// TestRenderTeamMemberRemovedAdmin_Interpolates proves the EN admin fan-out
// template actually interpolates both removed_member_name and team_name into
// the rendered output (guards against a static body).
func TestRenderTeamMemberRemovedAdmin_Interpolates(t *testing.T) {
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN
	params := map[string]string{"team_name": "Crew A", "removed_member_name": "Ivan"}
	title, body, err := Render(nt, params, "en")
	if err != nil {
		t.Fatalf("Render err: %v", err)
	}
	if !strings.Contains(title, "Ivan") {
		t.Errorf("title %q does not interpolate removed_member_name", title)
	}
	if !strings.Contains(body, "Ivan") || !strings.Contains(body, "Crew A") {
		t.Errorf("body %q does not interpolate both params", body)
	}
}

// TestIsVerbatim covers the verbatim-type predicate: only PLATFORM_MESSAGE is
// verbatim; every catalog type and the reserved types are not.
func TestIsVerbatim(t *testing.T) {
	if !IsVerbatim(notificationv1.NotificationType_NOTIFICATION_TYPE_PLATFORM_MESSAGE) {
		t.Error("PLATFORM_MESSAGE must be verbatim")
	}
	notVerbatim := []notificationv1.NotificationType{
		notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED,
		notificationv1.NotificationType_NOTIFICATION_TYPE_SYSTEM,
		notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED,
		notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED,
	}
	for _, nt := range notVerbatim {
		if IsVerbatim(nt) {
			t.Errorf("%s must not be verbatim", nt)
		}
	}
	// Every catalog type must be non-verbatim (verbatim types are NOT cataloged).
	for nt := range typeKey {
		if IsVerbatim(nt) {
			t.Errorf("cataloged type %s must not be verbatim", nt)
		}
	}
}

// TestRenderVerbatim covers the free-text render: literal copy of title/body,
// and ErrEmptyVerbatimText when both are empty.
func TestRenderVerbatim(t *testing.T) {
	t.Run("copies literal text", func(t *testing.T) {
		title, body, err := RenderVerbatim(map[string]string{
			"title": "Maintenance window",
			"body":  "The app will be down at 02:00.",
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if title != "Maintenance window" || body != "The app will be down at 02:00." {
			t.Errorf("got title=%q body=%q", title, body)
		}
	})
	t.Run("title only is valid", func(t *testing.T) {
		title, body, err := RenderVerbatim(map[string]string{"title": "Hi"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if title != "Hi" || body != "" {
			t.Errorf("got title=%q body=%q", title, body)
		}
	})
	t.Run("both empty rejected", func(t *testing.T) {
		_, _, err := RenderVerbatim(map[string]string{})
		assertReason(t, err, ReasonEmptyVerbatim)
	})
}

// TestExtractParams_SendPlatformMessage locks the verbatim payload extract:
// it returns the literal title/body and never falls through to ErrEmptyPayload.
func TestExtractParams_SendPlatformMessage(t *testing.T) {
	env := &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendPlatformMessage{
			SendPlatformMessage: &notificationv1.SendPlatformMessage{
				Title: "Hello", Body: "World",
			},
		},
	}
	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("ExtractParams err: %v", err)
	}
	if params["title"] != "Hello" || params["body"] != "World" {
		t.Errorf("params = %v, want title=Hello body=World", params)
	}
	// PLATFORM_MESSAGE must NOT be in the catalog: Render rejects it as unknown.
	_, _, rerr := Render(notificationv1.NotificationType_NOTIFICATION_TYPE_PLATFORM_MESSAGE, params, "en")
	assertReason(t, rerr, ReasonUnknownType)
}

// TestCatalogVsTemplateConsistency parses each locale's TOML file and
// verifies every {{.field}} placeholder is in requiredParams[T] for the
// matching type, and vice versa.
func TestCatalogVsTemplateConsistency(t *testing.T) {
	placeholderRe := regexp.MustCompile(`\{\{\.([a-z_]+)\}\}`)
	for _, loc := range []string{"en", "ru", "kk"} {
		loc := loc
		t.Run(loc, func(t *testing.T) {
			// loc is a hard-coded literal from the slice above, not user input.
			data, err := os.ReadFile("translations/" + loc + ".toml") // #nosec G304
			if err != nil {
				t.Fatalf("read translations: %v", err)
			}
			content := string(data)
			for nt, key := range typeKey {
				placeholdersInTemplate := map[string]bool{}
				section := extractSection(content, key)
				for _, m := range placeholderRe.FindAllStringSubmatch(section, -1) {
					placeholdersInTemplate[m[1]] = true
				}
				required := requiredParams[nt]
				requiredSet := map[string]bool{}
				for _, r := range required {
					requiredSet[r] = true
				}
				for p := range placeholdersInTemplate {
					if !requiredSet[p] {
						t.Errorf("%s/%s: placeholder %q in TOML but not in requiredParams",
							loc, key, p)
					}
				}
				for r := range requiredSet {
					if !placeholdersInTemplate[r] {
						t.Errorf("%s/%s: required param %q not used in TOML placeholders",
							loc, key, r)
					}
				}
			}
		})
	}
}

// extractSection returns the lines belonging to a single [<section>] block.
func extractSection(content, section string) string {
	marker := "[" + section + "]"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx:]
	nextHeader := regexp.MustCompile(`\n\[[a-z_]+\]`).FindStringIndex(rest[1:])
	if nextHeader == nil {
		return rest
	}
	return rest[:nextHeader[0]+1]
}
