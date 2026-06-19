package notifyrender

import (
	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// ExtractParams reads the typed payload from an envelope and returns
// a params map suitable for Render. Recipient-specific metadata fields
// stay on the envelope; this is only template fill-in fields.
//
// Returns ErrEmptyPayload() when env.Payload is unset.
//
// The function is a flat dispatch table over the oneof payload variants;
// each case is a 1-3 line literal map. Splitting it into helpers would
// hurt readability without reducing real complexity — the gocyclo score
// reflects the number of payload variants, not branching logic.
//
//nolint:gocyclo // flat dispatch over oneof payload variants; intentional.
func ExtractParams(env *notificationv1.NotificationEnvelope) (map[string]string, error) {
	if env == nil || env.Payload == nil {
		return nil, ErrEmptyPayload()
	}
	switch p := env.Payload.(type) {
	// ─── EXISTING payloads ───
	case *notificationv1.NotificationEnvelope_SendWelcome:
		return map[string]string{
			"user_name": p.SendWelcome.GetUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendEmailOtp:
		return map[string]string{
			"email": p.SendEmailOtp.GetEmail(),
			"code":  p.SendEmailOtp.GetCode(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendEmailVerified:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendInviteExistingUser:
		return map[string]string{
			"team_name":    p.SendInviteExistingUser.GetTeamName(),
			"inviter_name": p.SendInviteExistingUser.GetInviterName(),
			"role":         p.SendInviteExistingUser.GetRole(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteSms:
		return map[string]string{
			"team_name":    p.SendInviteSms.GetTeamName(),
			"inviter_name": p.SendInviteSms.GetInviterName(),
			"role":         p.SendInviteSms.GetRole(),
			"invite_url":   p.SendInviteSms.GetInviteUrl(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteAccepted:
		return map[string]string{
			"phone": p.SendInviteAccepted.GetPhone(),
			"role":  p.SendInviteAccepted.GetRole(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteDeclined:
		return map[string]string{
			"phone": p.SendInviteDeclined.GetPhone(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteExpired:
		return map[string]string{
			"phone": p.SendInviteExpired.GetPhone(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteUserRegistered:
		return map[string]string{
			"team_name": p.SendInviteUserRegistered.GetTeamName(),
			"phone":     p.SendInviteUserRegistered.GetPhone(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferredOld:
		// team_name was added to the payload (slice 2) so the in-app body can
		// name the team instead of rendering a static "you transferred the team".
		return map[string]string{
			"team_name": p.SendAdminTransferredOld.GetTeamName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferredNew:
		return map[string]string{
			"team_name": p.SendAdminTransferredNew.GetTeamName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantCreated:
		return map[string]string{
			"organization_name": p.SendTenantCreated.GetOrganizationName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantApproved:
		return map[string]string{
			"organization_name": p.SendTenantApproved.GetOrganizationName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantRejected:
		// SendTenantRejected proto only carries tenant_id + reason; organization_name
		// is not on the wire here (unlike SendTenantApproved/Created). Surface what's
		// actually present so callers don't get a phantom empty key.
		return map[string]string{
			"reason": p.SendTenantRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferInitiated:
		return map[string]string{
			"organization_name": p.SendAdminTransferInitiated.GetOrganizationName(),
			"from_user_name":    p.SendAdminTransferInitiated.GetFromUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferAccepted:
		return map[string]string{
			"organization_name": p.SendAdminTransferAccepted.GetOrganizationName(),
			"to_user_name":      p.SendAdminTransferAccepted.GetToUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferRejected:
		return map[string]string{
			"organization_name": p.SendAdminTransferRejected.GetOrganizationName(),
			"to_user_name":      p.SendAdminTransferRejected.GetToUserName(),
			"reason":            p.SendAdminTransferRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferCancelled:
		return map[string]string{
			"organization_name": p.SendAdminTransferCancelled.GetOrganizationName(),
			"from_user_name":    p.SendAdminTransferCancelled.GetFromUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendAdminTransferExpired:
		return map[string]string{
			"organization_name": p.SendAdminTransferExpired.GetOrganizationName(),
			"counterparty_name": p.SendAdminTransferExpired.GetCounterpartyName(),
		}, nil

	// ─── NEW payloads ───
	case *notificationv1.NotificationEnvelope_SendChatMessage:
		return map[string]string{
			"sender_name": p.SendChatMessage.GetSenderName(),
			"preview":     p.SendChatMessage.GetPreview(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendListingApproved:
		return map[string]string{
			"listing_title": p.SendListingApproved.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendListingRejected:
		return map[string]string{
			"listing_title": p.SendListingRejected.GetListingTitle(),
			"reason":        p.SendListingRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendFavoritePriceChanged:
		return map[string]string{
			"listing_title": p.SendFavoritePriceChanged.GetListingTitle(),
			"old_price":     p.SendFavoritePriceChanged.GetOldPrice(),
			"new_price":     p.SendFavoritePriceChanged.GetNewPrice(),
			"currency":      p.SendFavoritePriceChanged.GetCurrency(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendFavoriteListingRemoved:
		return map[string]string{
			"listing_title": p.SendFavoriteListingRemoved.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendListingUnpublished:
		return map[string]string{
			"listing_title": p.SendListingUnpublished.GetListingTitle(),
			"reason":        p.SendListingUnpublished.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantVerified:
		return map[string]string{
			"organization_name": p.SendTenantVerified.GetOrganizationName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOperatorAssigned:
		return map[string]string{
			"operator_name": p.SendOperatorAssigned.GetOperatorName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOperatorReleased:
		return map[string]string{
			"operator_name": p.SendOperatorReleased.GetOperatorName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendWalletOperationRequested:
		return map[string]string{
			"amount":         p.SendWalletOperationRequested.GetAmount(),
			"currency":       p.SendWalletOperationRequested.GetCurrency(),
			"operation_kind": p.SendWalletOperationRequested.GetOperationKind(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendWalletOperationDecided:
		return map[string]string{
			"amount":   p.SendWalletOperationDecided.GetAmount(),
			"currency": p.SendWalletOperationDecided.GetCurrency(),
			"decision": p.SendWalletOperationDecided.GetDecision(),
		}, nil

	// ─── team membership lifecycle payloads (slice 4) ───
	case *notificationv1.NotificationEnvelope_SendMemberBlocked:
		return map[string]string{
			"team_name": p.SendMemberBlocked.GetTeamName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendMemberUnblocked:
		return map[string]string{
			"team_name": p.SendMemberUnblocked.GetTeamName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendMemberRemoved:
		return map[string]string{
			"team_name": p.SendMemberRemoved.GetTeamName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTeamMemberRemovedAdmin:
		return map[string]string{
			"team_name":           p.SendTeamMemberRemovedAdmin.GetTeamName(),
			"removed_member_name": p.SendTeamMemberRemovedAdmin.GetRemovedMemberName(),
		}, nil

	// ─── free-text platform message (slice 5, verbatim) ───
	// Unlike every other payload this carries the literal title/body that the
	// consumer stores verbatim. ExtractParams surfaces them so the producer-side
	// validator (notifyoutbox) sees a non-empty payload; the inbox consumer
	// branches on IsVerbatim and copies them straight into the row, bypassing the
	// catalog template lookup (PLATFORM_MESSAGE is deliberately not in typeKey).
	case *notificationv1.NotificationEnvelope_SendPlatformMessage:
		return map[string]string{
			"title": p.SendPlatformMessage.GetTitle(),
			"body":  p.SendPlatformMessage.GetBody(),
		}, nil

	// ─── auth-service payloads ───
	case *notificationv1.NotificationEnvelope_SendLoginOtpSms:
		return map[string]string{
			"phone": p.SendLoginOtpSms.GetPhone(),
			"code":  p.SendLoginOtpSms.GetCode(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendLoginOtpEmail:
		return map[string]string{
			"email": p.SendLoginOtpEmail.GetEmail(),
			"code":  p.SendLoginOtpEmail.GetCode(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendNewDeviceLogin:
		return map[string]string{
			"device_name": p.SendNewDeviceLogin.GetDeviceName(),
			"ip":          p.SendNewDeviceLogin.GetIp(),
		}, nil
	default:
		return nil, ErrEmptyPayload()
	}
}
