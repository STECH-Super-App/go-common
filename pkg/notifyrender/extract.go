package notifyrender

import (
	"strings"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// ExtractParams reads the typed payload from an envelope and returns
// a params map suitable for Render. Recipient-specific metadata fields
// stay on the envelope; this is only template fill-in fields.
//
// Returns ErrEmptyPayload() when env.Payload is nil (no payload set at all),
// and ErrUnknownType() when a payload IS set but no case maps it — a directive
// variant that reached this package without a dispatch entry. The two are kept
// distinct so the caller can tell "producer sent an empty envelope" apart from
// "we forgot to wire up this directive" (the latter used to masquerade as the
// former and get dead-lettered as unrecoverable).
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
			"tenant_name":  p.SendInviteExistingUser.GetTenantName(),
			"inviter_name": p.SendInviteExistingUser.GetInviterName(),
			"role":         p.SendInviteExistingUser.GetRole(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendInviteSms:
		return map[string]string{
			"tenant_name":  p.SendInviteSms.GetTenantName(),
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
			"tenant_name": p.SendInviteUserRegistered.GetTenantName(),
			"phone":       p.SendInviteUserRegistered.GetPhone(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferredOld:
		// tenant_name was added to the payload (slice 2) so the in-app body can
		// name the tenant instead of rendering a static "you transferred the tenant".
		return map[string]string{
			"tenant_name": p.SendOrgAdminTransferredOld.GetTenantName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferredNew:
		return map[string]string{
			"tenant_name": p.SendOrgAdminTransferredNew.GetTenantName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationCreated:
		return map[string]string{
			"organization_name": p.SendOrganisationCreated.GetOrganizationName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationApproved:
		return map[string]string{
			"organization_name": p.SendOrganisationApproved.GetOrganizationName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationRejected:
		// SendOrganisationRejected proto only carries organisation_id + reason; organization_name
		// is not on the wire here (unlike SendOrganisationApproved/Created). Surface what's
		// actually present so callers don't get a phantom empty key.
		return map[string]string{
			"reason": p.SendOrganisationRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationDocumentsRequested:
		// organisation-service EMAIL/SYSTEM directive: the organisation owner is
		// asked to resubmit verification documents. reasons is a repeated field;
		// join it into a single value since the params map is map[string]string.
		return map[string]string{
			"organisation_id": p.SendOrganisationDocumentsRequested.GetOrganisationId(),
			"comment":         p.SendOrganisationDocumentsRequested.GetComment(),
			"reasons":         strings.Join(p.SendOrganisationDocumentsRequested.GetReasons(), ", "),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferInitiated:
		return map[string]string{
			"organization_name": p.SendOrgAdminTransferInitiated.GetOrganizationName(),
			"from_user_name":    p.SendOrgAdminTransferInitiated.GetFromUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferAccepted:
		return map[string]string{
			"organization_name": p.SendOrgAdminTransferAccepted.GetOrganizationName(),
			"to_user_name":      p.SendOrgAdminTransferAccepted.GetToUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferRejected:
		return map[string]string{
			"organization_name": p.SendOrgAdminTransferRejected.GetOrganizationName(),
			"to_user_name":      p.SendOrgAdminTransferRejected.GetToUserName(),
			"reason":            p.SendOrgAdminTransferRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferCancelled:
		return map[string]string{
			"organization_name": p.SendOrgAdminTransferCancelled.GetOrganizationName(),
			"from_user_name":    p.SendOrgAdminTransferCancelled.GetFromUserName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrgAdminTransferExpired:
		return map[string]string{
			"organization_name": p.SendOrgAdminTransferExpired.GetOrganizationName(),
			"counterparty_name": p.SendOrgAdminTransferExpired.GetCounterpartyName(),
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
	case *notificationv1.NotificationEnvelope_SendOrganisationVerified:
		return map[string]string{
			"organization_name": p.SendOrganisationVerified.GetOrganizationName(),
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

	// ─── tenant membership lifecycle payloads (slice 4) ───
	case *notificationv1.NotificationEnvelope_SendMemberBlocked:
		return map[string]string{
			"tenant_name": p.SendMemberBlocked.GetTenantName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendMemberUnblocked:
		return map[string]string{
			"tenant_name": p.SendMemberUnblocked.GetTenantName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendMemberRemoved:
		return map[string]string{
			"tenant_name": p.SendMemberRemoved.GetTenantName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantMemberRemovedAdmin:
		return map[string]string{
			"tenant_name":         p.SendTenantMemberRemovedAdmin.GetTenantName(),
			"removed_member_name": p.SendTenantMemberRemovedAdmin.GetRemovedMemberName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendTenantMemberLeft:
		return map[string]string{
			"tenant_name": p.SendTenantMemberLeft.GetTenantName(),
			"member_name": p.SendTenantMemberLeft.GetMemberName(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendMemberRoleChanged:
		return map[string]string{
			"tenant_name": p.SendMemberRoleChanged.GetTenantName(),
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

	// ─── order lifecycle payloads (order-service contracts, oneof fields 51-62) ───
	case *notificationv1.NotificationEnvelope_SendOrderRequestCreated:
		return map[string]string{
			"listing_title": p.SendOrderRequestCreated.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderRequestAccepted:
		return map[string]string{
			"listing_title": p.SendOrderRequestAccepted.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderTermsAgreed:
		return map[string]string{
			"listing_title": p.SendOrderTermsAgreed.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderConfirmed:
		return map[string]string{
			"listing_title": p.SendOrderConfirmed.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderCounterOfferSent:
		return map[string]string{
			"listing_title": p.SendOrderCounterOfferSent.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderCounterOfferWithdrawn:
		return map[string]string{
			"listing_title": p.SendOrderCounterOfferWithdrawn.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderCancelled:
		return map[string]string{
			"listing_title": p.SendOrderCancelled.GetListingTitle(),
			"cancelled_by":  p.SendOrderCancelled.GetCancelledBy(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderAutoCancelled:
		return map[string]string{
			"listing_title": p.SendOrderAutoCancelled.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderTransferred:
		return map[string]string{
			"listing_title": p.SendOrderTransferred.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderReceiptConfirmed:
		return map[string]string{
			"listing_title": p.SendOrderReceiptConfirmed.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderAutoCompleted:
		return map[string]string{
			"listing_title": p.SendOrderAutoCompleted.GetListingTitle(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrderReviewWindowEnding:
		return map[string]string{
			"listing_title": p.SendOrderReviewWindowEnding.GetListingTitle(),
		}, nil

	// ─── delivery lifecycle payloads (order-service delivery vertical) ───
	// 9 of the 18 payloads carry request_no — the human-facing delivery request
	// number (Д-13). It is a catalog OPTIONAL param: the proto getter returns ""
	// for a directive emitted before numbering shipped, and the baseline renders
	// it behind an {{if}} guard, so an empty value degrades to the plain sentence
	// instead of a dangling "#". Surface it unconditionally — Render decides
	// whether it reaches the text. The remaining payloads carry no fields at all
	// and map to an empty map.
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestCreated:
		return map[string]string{
			"request_no": p.SendDeliveryRequestCreated.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestAccepted:
		// The emitter pre-formats final_price into a display string; pass it
		// through verbatim (notifyrender interpolates strings only).
		return map[string]string{
			"final_price": p.SendDeliveryRequestAccepted.GetFinalPrice(),
			"currency":    p.SendDeliveryRequestAccepted.GetCurrency(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestRejected:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryCounterOfferSent:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryCounterOfferAccepted:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryCounterOfferDeclined:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryCounterOfferWithdrawn:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestCancelled:
		// cancel_reason is optional (late-cancellation only) and not templated,
		// mirroring admin_transfer_rejected's optional reason — surface only
		// cancelled_by and request_no, the two declared params.
		return map[string]string{
			"cancelled_by": p.SendDeliveryRequestCancelled.GetCancelledBy(),
			"request_no":   p.SendDeliveryRequestCancelled.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryLoadingToday:
		return map[string]string{
			"route":      p.SendDeliveryLoadingToday.GetRoute(),
			"request_no": p.SendDeliveryLoadingToday.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestExpired:
		return map[string]string{
			"request_no": p.SendDeliveryRequestExpired.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryInTransit:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryAwaitingReceipt:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryReceiptReminder:
		return map[string]string{
			"request_no": p.SendDeliveryReceiptReminder.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryAutoConfirmed:
		return map[string]string{
			"request_no": p.SendDeliveryAutoConfirmed.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryRequestCompleted:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryReviewInvite:
		return map[string]string{
			"request_no": p.SendDeliveryReviewInvite.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryReviewWindowEnding:
		return map[string]string{
			"request_no": p.SendDeliveryReviewWindowEnding.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendDeliveryCascadeCancelled:
		return map[string]string{
			"request_no": p.SendDeliveryCascadeCancelled.GetRequestNo(),
		}, nil
	default:
		// A payload IS set (the nil case is caught by the guard at the top) but no
		// case matched it — a directive variant that reached this package without a
		// dispatch entry. Report it as unknown-type, NOT empty-payload: masquerading
		// as empty made the caller treat a merely-unmapped directive as unrecoverable
		// and dead-letter it. GetMetadata()/GetType() are nil-safe.
		return nil, ErrUnknownType(env.GetMetadata().GetType())
	}
}
