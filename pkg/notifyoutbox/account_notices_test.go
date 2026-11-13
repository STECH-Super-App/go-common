package notifyoutbox

import (
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// accountNoticeEnvelope builds an IN_APP envelope of one of the six typed
// account notices (179-184, OTDELRAZRABOTKI-965) deep-linked to the profile.
func accountNoticeEnvelope(t *testing.T, nt notificationv1.NotificationType,
	payload func(*notificationv1.NotificationEnvelope)) *notificationv1.NotificationEnvelope {
	t.Helper()
	e := validEnvelope(t)
	e.Metadata.Type = nt
	e.Metadata.DeepLink = &notificationv1.DeepLinkTarget{Screen: notificationv1.DeepLinkScreen_DEEP_LINK_SCREEN_PROFILE}
	payload(e)
	return e
}

// TestValidate_AccountNotices pins the publish gate for the six notices: the
// profile deep link is accepted, the required params are enforced (an empty
// value counts as missing) and the optional ones may stay empty.
func TestValidate_AccountNotices(t *testing.T) {
	const phone = "+77001234567"
	cases := []struct {
		name    string
		nt      notificationv1.NotificationType
		payload func(*notificationv1.NotificationEnvelope)
		wantErr string // missing param name, "" = must publish
	}{
		{"welcome", notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendWelcome{SendWelcome: &notificationv1.SendWelcome{UserName: "Aidar"}}
			}, ""},
		{"welcome without user_name", notificationv1.NotificationType_NOTIFICATION_TYPE_WELCOME,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendWelcome{SendWelcome: &notificationv1.SendWelcome{}}
			}, "user_name"},
		{"new_device_login knows nothing", notificationv1.NotificationType_NOTIFICATION_TYPE_NEW_DEVICE_LOGIN,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendNewDeviceLogin{SendNewDeviceLogin: &notificationv1.SendNewDeviceLogin{}}
			}, ""},
		{"organisation_submitted", notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendOrganisationCreated{
					SendOrganisationCreated: &notificationv1.SendOrganisationCreated{OrganizationName: "Acme LLC"}}
			}, ""},
		{"organisation_submitted without name", notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_SUBMITTED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendOrganisationCreated{
					SendOrganisationCreated: &notificationv1.SendOrganisationCreated{}}
			}, "organization_name"},
		{"documents_requested bare", notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendOrganisationDocumentsRequested{
					SendOrganisationDocumentsRequested: &notificationv1.SendOrganisationDocumentsRequested{
						OrganisationId: "org-1", OrganizationName: "Acme LLC"}}
			}, ""},
		{"documents_requested without name", notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DOCUMENTS_REQUESTED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendOrganisationDocumentsRequested{
					SendOrganisationDocumentsRequested: &notificationv1.SendOrganisationDocumentsRequested{
						OrganisationId: "org-1", Reasons: []string{"blurry scan"}}}
			}, "organization_name"},
		{"invite_expired", notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendInviteExpired{
					SendInviteExpired: &notificationv1.SendInviteExpired{Phone: phone, TenantName: "Acme Team"}}
			}, ""},
		{"invite_expired without tenant_name", notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_EXPIRED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendInviteExpired{
					SendInviteExpired: &notificationv1.SendInviteExpired{Phone: phone}}
			}, "tenant_name"},
		{"invite_invitee_registered", notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_INVITEE_REGISTERED,
			func(e *notificationv1.NotificationEnvelope) {
				e.Payload = &notificationv1.NotificationEnvelope_SendInviteUserRegistered{
					SendInviteUserRegistered: &notificationv1.SendInviteUserRegistered{TenantName: "Acme Team", Phone: phone}}
			}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := accountNoticeEnvelope(t, tc.nt, tc.payload)
			err := validate(e)
			if err == nil {
				err = validateParams(e)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want publishable, got %v", err)
				}
				return
			}
			assertReason(t, err, ReasonMissingParam)
		})
	}
}
