package notifyrender

import (
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// The six typed account notices (179-184, OTDELRAZRABOTKI-965). Before them
// these were EMAIL-only directives; a person may now register without an
// e-mail, so each is an in-app row (two also a push). The tests render the
// compiled-in English baseline — no overlay — so they pin the copy that ships
// when i18n-catalog has no entry for a key.

func TestAccountNotices_TypeKeys(t *testing.T) {
	for nt, want := range map[notificationv1.NotificationType]string{
		notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME:                          "welcome",
		notificationv1.NotificationType_NOTIFICATION_TYPE_NEW_DEVICE_LOGIN:                 "new_device_login",
		notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED:           "organisation_submitted",
		notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED: "organisation_documents_requested",
		notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED:                   "invite_expired",
		notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_INVITEE_REGISTERED:        "invite_invitee_registered",
	} {
		if got := typeKey[nt]; got != want {
			t.Errorf("typeKey[%v] = %q, want %q", nt, got, want)
		}
	}
}

func TestAccountNotices_ParamTiers(t *testing.T) {
	for _, tc := range []struct {
		nt       notificationv1.NotificationType
		required []string
		optional []string
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME, []string{"user_name"}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_NEW_DEVICE_LOGIN, nil, []string{"device_name", "ip"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED, []string{"organization_name"}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED, []string{"organization_name"}, []string{"reasons", "comment"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED, []string{"tenant_name", "phone"}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_INVITEE_REGISTERED, []string{"tenant_name", "phone"}, nil},
	} {
		if got := RequiredParams(tc.nt); !equalStrings(got, tc.required) {
			t.Errorf("%v required = %v, want %v", tc.nt, got, tc.required)
		}
		if got := OptionalParams(tc.nt); !equalStrings(got, tc.optional) {
			t.Errorf("%v optional = %v, want %v", tc.nt, got, tc.optional)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAccountNotices_RenderBaseline renders every notice with and without each
// optional param and compares the exact English text. The optional-absent rows
// matter most: the sentence must still read well, with no dangling «()», «#»,
// «Reasons: .» or «<no value>».
func TestAccountNotices_RenderBaseline(t *testing.T) {
	r := newTestRenderer(t, nil)
	const (
		welcome   = notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME
		device    = notificationv1.NotificationType_NOTIFICATION_TYPE_NEW_DEVICE_LOGIN
		submitted = notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED
		docs      = notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED
		expired   = notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED
		invitee   = notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_INVITEE_REGISTERED
	)
	for _, tc := range []struct {
		name      string
		nt        notificationv1.NotificationType
		params    map[string]string
		wantTitle string
		wantBody  string
	}{
		{"welcome", welcome, map[string]string{"user_name": "Aidar"},
			"Welcome to roybee",
			"Aidar, your account is ready. You can add an e-mail in your profile."},

		{"new_device/all", device, map[string]string{"device_name": "Pixel 8", "ip": "203.0.113.7"},
			"New sign-in to your account",
			"Signed in from Pixel 8 (IP 203.0.113.7). If it wasn't you, contact support."},
		{"new_device/no_ip", device, map[string]string{"device_name": "Pixel 8", "ip": ""},
			"New sign-in to your account",
			"Signed in from Pixel 8. If it wasn't you, contact support."},
		{"new_device/no_device", device, map[string]string{"device_name": "", "ip": "203.0.113.7"},
			"New sign-in to your account",
			"Signed in from IP 203.0.113.7. If it wasn't you, contact support."},
		{"new_device/neither", device, map[string]string{"device_name": "", "ip": ""},
			"New sign-in to your account",
			"A new sign-in to your account was detected. If it wasn't you, contact support."},
		{"new_device/keys_absent", device, map[string]string{},
			"New sign-in to your account",
			"A new sign-in to your account was detected. If it wasn't you, contact support."},

		{"submitted", submitted, map[string]string{"organization_name": "Acme LLC"},
			"Organization sent for review",
			"Acme LLC has been sent for verification. We'll tell you the result."},

		{"docs/all", docs, map[string]string{"organization_name": "Acme LLC", "reasons": "blurry scan, expired license", "comment": "please resubmit"},
			"Documents required",
			"Acme LLC: additional documents are required. Reasons: blurry scan, expired license. Comment: please resubmit"},
		{"docs/no_comment", docs, map[string]string{"organization_name": "Acme LLC", "reasons": "blurry scan", "comment": ""},
			"Documents required",
			"Acme LLC: additional documents are required. Reasons: blurry scan."},
		{"docs/no_reasons", docs, map[string]string{"organization_name": "Acme LLC", "reasons": "", "comment": "please resubmit"},
			"Documents required",
			"Acme LLC: additional documents are required. Comment: please resubmit"},
		{"docs/neither", docs, map[string]string{"organization_name": "Acme LLC", "reasons": "", "comment": ""},
			"Documents required",
			"Acme LLC: additional documents are required."},
		{"docs/keys_absent", docs, map[string]string{"organization_name": "Acme LLC"},
			"Documents required",
			"Acme LLC: additional documents are required."},

		{"expired", expired, map[string]string{"tenant_name": "Acme Team", "phone": "+77001234567"},
			"Invitation expired",
			"The invitation to Acme Team for +77001234567 has expired. You can send a new one."},

		{"invitee", invitee, map[string]string{"tenant_name": "Acme Team", "phone": "+77001234567"},
			"Invited person joined",
			"+77001234567 has registered and can now accept your invitation to Acme Team."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title, body, err := r.Render(tc.nt, tc.params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if title != tc.wantTitle {
				t.Errorf("title = %q, want %q", title, tc.wantTitle)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			for _, s := range []string{title, body} {
				if strings.Contains(s, "<no value>") || strings.Contains(strings.ToLower(s), "stech") {
					t.Errorf("rendered text %q carries a template artefact or the codename", s)
				}
			}
		})
	}
}

// TestAccountNotices_RequiredParamRejected: the required tier is enforced by
// Render (and, at publish time, by notifyoutbox), the optional tier is not.
func TestAccountNotices_RequiredParamRejected(t *testing.T) {
	r := newTestRenderer(t, nil)
	for _, tc := range []struct {
		nt      notificationv1.NotificationType
		missing string
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME, "user_name"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED, "organization_name"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED, "organization_name"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED, "tenant_name"},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_INVITEE_REGISTERED, "phone"},
	} {
		if _, _, err := r.Render(tc.nt, map[string]string{}, "en"); err == nil {
			t.Errorf("%v rendered without required %q", tc.nt, tc.missing)
		} else {
			assertReason(t, err, ReasonMissingParam)
		}
	}
}

// TestExtractParams_AccountNotices locks the extract arm of each of the six
// payloads, including the two fields added for them: organization_name on
// SendOrganisationDocumentsRequested and tenant_name on SendInviteExpired.
func TestExtractParams_AccountNotices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload *notificationv1.NotificationEnvelope
		want    map[string]string
	}{
		{"welcome", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendWelcome{
			SendWelcome: &notificationv1.SendWelcome{UserName: "Aidar"}}},
			map[string]string{"user_name": "Aidar"}},
		{"new_device_login", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendNewDeviceLogin{
			SendNewDeviceLogin: &notificationv1.SendNewDeviceLogin{DeviceName: "Pixel 8", Ip: "203.0.113.7"}}},
			map[string]string{"device_name": "Pixel 8", "ip": "203.0.113.7"}},
		{"organisation_submitted", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendOrganisationCreated{
			SendOrganisationCreated: &notificationv1.SendOrganisationCreated{OrganizationName: "Acme LLC"}}},
			map[string]string{"organization_name": "Acme LLC"}},
		{"organisation_documents_requested", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendOrganisationDocumentsRequested{
			SendOrganisationDocumentsRequested: &notificationv1.SendOrganisationDocumentsRequested{
				OrganisationId: "org-1", OrganizationName: "Acme LLC", Reasons: []string{"a", "b"}, Comment: "c"}}},
			map[string]string{"organisation_id": "org-1", "organization_name": "Acme LLC", "reasons": "a, b", "comment": "c"}},
		{"invite_expired", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendInviteExpired{
			SendInviteExpired: &notificationv1.SendInviteExpired{Phone: "+77001234567", TenantName: "Acme Team"}}},
			map[string]string{"phone": "+77001234567", "tenant_name": "Acme Team"}},
		{"invite_invitee_registered", &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendInviteUserRegistered{
			SendInviteUserRegistered: &notificationv1.SendInviteUserRegistered{TenantName: "Acme Team", Phone: "+77001234567"}}},
			map[string]string{"tenant_name": "Acme Team", "phone": "+77001234567"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractParams(tc.payload)
			if err != nil {
				t.Fatalf("ExtractParams: %v", err)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("params[%q] = %q, want %q", k, got[k], v)
				}
			}
			for k := range got {
				if _, ok := tc.want[k]; !ok {
					t.Errorf("unexpected param %q = %q", k, got[k])
				}
			}
		})
	}
}

// TestAccountNotices_ExtractThenRender runs the legacy-shaped payload through
// ExtractParams and Render: a directive whose optional-bearing payload fields
// are empty (an old sender, or one that does not know the device) still renders.
func TestAccountNotices_ExtractThenRender(t *testing.T) {
	r := newTestRenderer(t, nil)
	env := &notificationv1.NotificationEnvelope{Payload: &notificationv1.NotificationEnvelope_SendNewDeviceLogin{
		SendNewDeviceLogin: &notificationv1.SendNewDeviceLogin{}}}
	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("ExtractParams: %v", err)
	}
	_, body, err := r.Render(notificationv1.NotificationType_NOTIFICATION_TYPE_NEW_DEVICE_LOGIN, params, "en")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "A new sign-in to your account was detected. If it wasn't you, contact support."; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}
