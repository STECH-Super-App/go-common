package notifyrender

import (
	"strconv"
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

	// ─── organisation change-request moderation (organisation-service) ───
	// The four directives that close a change request. submitted_at arrives as an
	// already-formatted ISO-8601 date (YYYY-MM-DD) and is passed through verbatim;
	// reasons is a repeated field joined into one value since params is
	// map[string]string. comment is surfaced for the EMAIL templates that want it
	// but is deliberately NOT a required param — a moderator may leave it blank,
	// and notifyoutbox treats an empty required param as missing.
	case *notificationv1.NotificationEnvelope_SendOrganisationChangeApproved:
		return map[string]string{
			"organization_name": p.SendOrganisationChangeApproved.GetOrganizationName(),
			"submitted_at":      p.SendOrganisationChangeApproved.GetSubmittedAt(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationChangeRejected:
		return map[string]string{
			"organization_name": p.SendOrganisationChangeRejected.GetOrganizationName(),
			"submitted_at":      p.SendOrganisationChangeRejected.GetSubmittedAt(),
			"reasons":           strings.Join(p.SendOrganisationChangeRejected.GetReasons(), ", "),
			"comment":           p.SendOrganisationChangeRejected.GetComment(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationChangeDocumentsRequested:
		return map[string]string{
			"organization_name": p.SendOrganisationChangeDocumentsRequested.GetOrganizationName(),
			"submitted_at":      p.SendOrganisationChangeDocumentsRequested.GetSubmittedAt(),
			"reasons":           strings.Join(p.SendOrganisationChangeDocumentsRequested.GetReasons(), ", "),
			"comment":           p.SendOrganisationChangeDocumentsRequested.GetComment(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendOrganisationContactsChanged:
		return map[string]string{
			"organization_name": p.SendOrganisationContactsChanged.GetOrganizationName(),
		}, nil

	// ─── organisation deactivated (П-14) ───
	// The org's managers are told it was deactivated because its leading user
	// deleted their account. organisation_id is on the wire but deliberately not
	// surfaced: it routes the deep link, it does not fill a template.
	case *notificationv1.NotificationEnvelope_SendOrganisationDeactivated:
		return map[string]string{
			"organization_name": p.SendOrganisationDeactivated.GetOrganizationName(),
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

	// ─── Маркетплейс запчастей (parts) ───
	//
	// The first payloads in this package carrying NUMERIC fields. notifyrender
	// interpolates strings only, so a REQUIRED count goes through strconv.Itoa — a
	// zero becomes "0", which renders and, unlike an empty string, satisfies
	// notifyoutbox's required-param check. An OPTIONAL count goes through
	// countOrEmpty instead; see its doc comment for why the two cannot share a
	// formatter.
	//
	// NO ARM READS A SHOP NAME. `shop_name` is `reserved` on all 53 SendParts*
	// messages (OWNER-ANSWERS-2026-08-31 B-5) because М-01 makes the shop's name
	// the company's name and every payload already carries the tenant a client
	// resolves it from. Re-adding one here is foreclosed, not merely unnecessary.
	//
	// NO ARM READS A CURRENT-PRICE `currency` EITHER. DECISIONS §30.2 removed
	// currency everywhere; three of these payloads still DECLARE the field because
	// the proto predates the ruling, and the PHP builder never sets it — so
	// reading it would put an empty string on a template that has one currency.
	// The «₽» in the baseline is a rendering, which is exactly what §30.2 says it
	// is.
	//
	// NO ARM READS AN OFFER ID. Every one of these directives carries its anchor
	// in `metadata.deep_link`, which is where a client reads it; the payload id is
	// not display copy. That also sidesteps a live drift: the pinned gen-go-lib
	// still generates `OfferRef` while sale-service already emits `offer_id`, so
	// the field arrives as "" under DiscardUnknown. Recorded in
	// docs/parts-part2/OPEN-DURING-BUILD.md; nothing here depends on it.
	case *notificationv1.NotificationEnvelope_SendPartsOfferHiddenByAdmin:
		return map[string]string{
			"product_name": p.SendPartsOfferHiddenByAdmin.GetProductName(),
			"reason":       p.SendPartsOfferHiddenByAdmin.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOfferSanctionLifted:
		return map[string]string{
			"product_name": p.SendPartsOfferSanctionLifted.GetProductName(),
		}, nil
	// TWO PARAMS FROM ONE FIELD, and that is the whole point of `O-67`. The wire
	// carries a LIST; `causesOrEmpty` flattens it to one string for the generic
	// guard, and once flat a template can ask «is anything left?» but not «is
	// price age among them?» — Go's text/template has no substring test in its
	// default function set and this package registers none. Deriving the second
	// param HERE costs no contract change, because the list never left.
	case *notificationv1.NotificationEnvelope_SendPartsShopSanctionLifted:
		causes := p.SendPartsShopSanctionLifted.GetRemainingCauses()

		return map[string]string{
			"remaining_causes":      causesOrEmpty(causes),
			"remaining_price_stale": causePresent(causes, causePriceStale),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsShopVerificationRestored:
		causes := p.SendPartsShopVerificationRestored.GetRemainingCauses()

		return map[string]string{
			"remaining_causes":      causesOrEmpty(causes),
			"remaining_price_stale": causePresent(causes, causePriceStale),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOfferBackInStock:
		return map[string]string{
			"product_name": p.SendPartsOfferBackInStock.GetProductName(),
			"price":        p.SendPartsOfferBackInStock.GetPrice(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsFavoritePriceDropped:
		return map[string]string{
			"product_name": p.SendPartsFavoritePriceDropped.GetProductName(),
			"old_price":    p.SendPartsFavoritePriceDropped.GetOldPrice(),
			"new_price":    p.SendPartsFavoritePriceDropped.GetNewPrice(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSubscriptionOfferAppeared:
		return map[string]string{
			"product_name": p.SendPartsSubscriptionOfferAppeared.GetProductName(),
			"price_from":   p.SendPartsSubscriptionOfferAppeared.GetPriceFrom(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSubscriptionExpiring:
		return map[string]string{
			"product_name": p.SendPartsSubscriptionExpiring.GetProductName(),
			// REQUIRED, so strconv.Itoa and never countOrEmpty: a subscription
			// swept on its last day legitimately carries 0, and blanking it would
			// reject the directive at publish time.
			"days_left": strconv.Itoa(int(p.SendPartsSubscriptionExpiring.GetDaysLeft())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsPriceListStaleWarning:
		return map[string]string{
			"days_since_upload": strconv.Itoa(int(p.SendPartsPriceListStaleWarning.GetDaysSinceUpload())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOffersHiddenPriceListStale:
		return map[string]string{
			"days_since_upload": strconv.Itoa(int(p.SendPartsOffersHiddenPriceListStale.GetDaysSinceUpload())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsPriceListProcessed:
		return map[string]string{
			"published_count":   strconv.Itoa(int(p.SendPartsPriceListProcessed.GetPublishedCount())),
			"matching_count":    strconv.Itoa(int(p.SendPartsPriceListProcessed.GetMatchingCount())),
			"error_count":       strconv.Itoa(int(p.SendPartsPriceListProcessed.GetErrorCount())),
			"new_address_count": countOrEmpty(p.SendPartsPriceListProcessed.GetNewAddressCount()),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsPriceListFileFailed:
		return map[string]string{
			"file_name": p.SendPartsPriceListFileFailed.GetFileName(),
		}, nil

	// ─── D-16a: the order, sourcing, catalogue, review and shop-level arms ───
	//
	// Same three prohibitions as above — no shop name, no currency, no id — and one
	// addition of its own.
	//
	// AN ENUM ON THE WIRE BECOMES A PAIR OF FLAGS HERE, NEVER A PARAM. Four fields
	// pick between editions of a sentence rather than appearing in one:
	// `fulfilment_kind` (PICKUP | CARRIER), `deadline_basis` (CALENDAR |
	// FROM_PAYMENT), `partial_kind` (POSITIONS_REMOVED | QUANTITY_REDUCED) and the
	// В-66 complaint `outcome` (HIDDEN | NO_VIOLATION). Each is turned into "1"/""
	// flags by flagWhen, exactly as `remaining_price_stale` is by causePresent, and
	// for the same stated reason: the template can then ask a question the
	// renderer's function set cannot answer, and the catalog validators — which
	// only ever see a literal {{.name}} — keep working.
	//
	// BOTH ARMS GET A FLAG. `{{if .is_pickup}}…{{else}}…{{end}}` would be shorter
	// and is wrong: an absent or unrecognised `fulfilment_kind` falls into the
	// else, and the buyer of a PICKUP order is then told his parcel is on its way.
	// With a flag on each arm the unknown value lights neither, the clause is
	// simply not printed, and the sentence stays true — which is the only failure
	// mode a push may have. TestPartsBranchFlagsNeverAssertTheWrongEdition pins it.
	case *notificationv1.NotificationEnvelope_SendPartsPositionsMatched:
		return map[string]string{
			"published_count": strconv.Itoa(int(p.SendPartsPositionsMatched.GetPublishedCount())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsPositionsPublishedRetro:
		return map[string]string{
			"published_count": strconv.Itoa(int(p.SendPartsPositionsPublishedRetro.GetPublishedCount())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsPositionsRejected:
		return map[string]string{
			"rejected_count": strconv.Itoa(int(p.SendPartsPositionsRejected.GetRejectedCount())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsShopHiddenByAdmin:
		return map[string]string{
			"reason": p.SendPartsShopHiddenByAdmin.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsShopVerifiedBadgeRevoked:
		return map[string]string{}, nil
	case *notificationv1.NotificationEnvelope_SendPartsShopUnavailableOrderNotice:
		return map[string]string{
			"order_no": p.SendPartsShopUnavailableOrderNotice.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderCreated:
		return map[string]string{
			"order_no":            p.SendPartsOrderCreated.GetOrderNo(),
			"product_name":        p.SendPartsOrderCreated.GetProductName(),
			"position_count":      strconv.Itoa(int(p.SendPartsOrderCreated.GetPositionCount())),
			"total":               p.SendPartsOrderCreated.GetTotal(),
			"sourcing_request_no": p.SendPartsOrderCreated.GetSourcingRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderConfirmed:
		kind := p.SendPartsOrderConfirmed.GetFulfilmentKind()

		return map[string]string{
			"order_no":        p.SendPartsOrderConfirmed.GetOrderNo(),
			"ready_date":      p.SendPartsOrderConfirmed.GetReadyDate(),
			"is_pickup":       flagWhen(kind, fulfilmentPickup),
			"is_carrier":      flagWhen(kind, fulfilmentCarrier),
			"is_from_payment": flagWhen(p.SendPartsOrderConfirmed.GetDeadlineBasis(), deadlineFromPayment),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderConfirmedPartially:
		partial := p.SendPartsOrderConfirmedPartially.GetPartialKind()

		return map[string]string{
			"order_no":             p.SendPartsOrderConfirmedPartially.GetOrderNo(),
			"confirmed_count":      strconv.Itoa(int(p.SendPartsOrderConfirmedPartially.GetConfirmedCount())),
			"total_count":          strconv.Itoa(int(p.SendPartsOrderConfirmedPartially.GetTotalCount())),
			"is_positions_removed": flagWhen(partial, partialPositionsRemoved),
			"is_quantity_reduced":  flagWhen(partial, partialQuantityReduced),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderFulfilmentOverdueSeller:
		kind := p.SendPartsOrderFulfilmentOverdueSeller.GetFulfilmentKind()

		return map[string]string{
			"order_no":      p.SendPartsOrderFulfilmentOverdueSeller.GetOrderNo(),
			"deadline_date": p.SendPartsOrderFulfilmentOverdueSeller.GetDeadlineDate(),
			"is_pickup":     flagWhen(kind, fulfilmentPickup),
			"is_carrier":    flagWhen(kind, fulfilmentCarrier),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderFulfilmentOverdueBuyer:
		kind := p.SendPartsOrderFulfilmentOverdueBuyer.GetFulfilmentKind()

		return map[string]string{
			"order_no":   p.SendPartsOrderFulfilmentOverdueBuyer.GetOrderNo(),
			"is_pickup":  flagWhen(kind, fulfilmentPickup),
			"is_carrier": flagWhen(kind, fulfilmentCarrier),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderDispatchReminder:
		return map[string]string{
			"order_no": p.SendPartsOrderDispatchReminder.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderReadyForPickup:
		return map[string]string{
			"order_no":       p.SendPartsOrderReadyForPickup.GetOrderNo(),
			"pickup_address": p.SendPartsOrderReadyForPickup.GetPickupAddress(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderHandedToCarrier:
		return map[string]string{
			"order_no":        p.SendPartsOrderHandedToCarrier.GetOrderNo(),
			"carrier_name":    p.SendPartsOrderHandedToCarrier.GetCarrierName(),
			"tracking_number": p.SendPartsOrderHandedToCarrier.GetTrackingNumber(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderPickedUp:
		return map[string]string{
			"order_no": p.SendPartsOrderPickedUp.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderReceiptReminder:
		return map[string]string{
			"order_no": p.SendPartsOrderReceiptReminder.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderReceiptConfirmed:
		return map[string]string{
			"order_no": p.SendPartsOrderReceiptConfirmed.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderAutoConfirmedBuyer:
		return map[string]string{
			"order_no": p.SendPartsOrderAutoConfirmedBuyer.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderAutoConfirmedSeller:
		return map[string]string{
			"order_no": p.SendPartsOrderAutoConfirmedSeller.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderRejectedBySeller:
		return map[string]string{
			"order_no": p.SendPartsOrderRejectedBySeller.GetOrderNo(),
			"reason":   p.SendPartsOrderRejectedBySeller.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderCancelledByBuyer:
		return map[string]string{
			"order_no": p.SendPartsOrderCancelledByBuyer.GetOrderNo(),
			"reason":   p.SendPartsOrderCancelledByBuyer.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderCancelledBySeller:
		return map[string]string{
			"order_no": p.SendPartsOrderCancelledBySeller.GetOrderNo(),
			"reason":   p.SendPartsOrderCancelledBySeller.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderResponseReminder:
		return map[string]string{
			"order_no": p.SendPartsOrderResponseReminder.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderExpiredBuyer:
		return map[string]string{
			"order_no": p.SendPartsOrderExpiredBuyer.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderExpiredSeller:
		return map[string]string{
			"order_no": p.SendPartsOrderExpiredSeller.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderIssueReported:
		return map[string]string{
			"order_no": p.SendPartsOrderIssueReported.GetOrderNo(),
			"reason":   p.SendPartsOrderIssueReported.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderIssueResolvedBuyer:
		return map[string]string{
			"order_no": p.SendPartsOrderIssueResolvedBuyer.GetOrderNo(),
			"outcome":  p.SendPartsOrderIssueResolvedBuyer.GetOutcome(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsOrderIssueResolvedSeller:
		return map[string]string{
			"order_no": p.SendPartsOrderIssueResolvedSeller.GetOrderNo(),
			"outcome":  p.SendPartsOrderIssueResolvedSeller.GetOutcome(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestCreated:
		return map[string]string{
			"request_no":     p.SendPartsSourcingRequestCreated.GetRequestNo(),
			"machinery_type": p.SendPartsSourcingRequestCreated.GetMachineryType(),
			"brand":          p.SendPartsSourcingRequestCreated.GetBrand(),
			"model":          p.SendPartsSourcingRequestCreated.GetModel(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingQuoteReceived:
		return map[string]string{
			"request_no":     p.SendPartsSourcingQuoteReceived.GetRequestNo(),
			"position_count": strconv.Itoa(int(p.SendPartsSourcingQuoteReceived.GetPositionCount())),
			"total":          p.SendPartsSourcingQuoteReceived.GetTotal(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingNoQuotesYet:
		return map[string]string{
			"request_no": p.SendPartsSourcingNoQuotesYet.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingAllShopsDeclined:
		return map[string]string{
			"request_no": p.SendPartsSourcingAllShopsDeclined.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestClosedByBuyer:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestClosedByBuyer.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestClosedByOrderBuyer:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestClosedByOrderBuyer.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestClosedByOrderShop:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestClosedByOrderShop.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestCancelled:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestCancelled.GetRequestNo(),
			"reason":     p.SendPartsSourcingRequestCancelled.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestExpiring:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestExpiring.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestExtended:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestExtended.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestExpiredBuyer:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestExpiredBuyer.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingRequestExpiredShop:
		return map[string]string{
			"request_no": p.SendPartsSourcingRequestExpiredShop.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingQuoteWithdrawnShopClosed:
		return map[string]string{
			"request_no": p.SendPartsSourcingQuoteWithdrawnShopClosed.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsSourcingQuoteWithdrawnByShop:
		return map[string]string{
			"request_no": p.SendPartsSourcingQuoteWithdrawnByShop.GetRequestNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsCatalogueMachineryAdded:
		return map[string]string{
			"machinery_type": p.SendPartsCatalogueMachineryAdded.GetMachineryType(),
			"brand":          p.SendPartsCatalogueMachineryAdded.GetBrand(),
			"model":          p.SendPartsCatalogueMachineryAdded.GetModel(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsCatalogueMachineryRejected:
		return map[string]string{
			"machinery_type": p.SendPartsCatalogueMachineryRejected.GetMachineryType(),
			"brand":          p.SendPartsCatalogueMachineryRejected.GetBrand(),
			"model":          p.SendPartsCatalogueMachineryRejected.GetModel(),
			"reason":         p.SendPartsCatalogueMachineryRejected.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsReviewInvite:
		return map[string]string{
			"order_no": p.SendPartsReviewInvite.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsReviewReminder:
		return map[string]string{
			"order_no": p.SendPartsReviewReminder.GetOrderNo(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsReviewReceived:
		return map[string]string{
			"rating": strconv.Itoa(int(p.SendPartsReviewReceived.GetRating())),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsReviewHiddenByComplaint:
		return map[string]string{
			"reason": p.SendPartsReviewHiddenByComplaint.GetReason(),
		}, nil
	case *notificationv1.NotificationEnvelope_SendPartsReviewComplaintResolved:
		outcome := p.SendPartsReviewComplaintResolved.GetOutcome()

		return map[string]string{
			"outcome_hidden":       flagWhen(outcome, complaintOutcomeHidden),
			"outcome_no_violation": flagWhen(outcome, complaintOutcomeNoViolation),
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

// countOrEmpty formats an OPTIONAL int32 count for a baseline that reads it
// behind an {{if}} guard. A zero becomes "" rather than "0" because Go's template
// truth test runs on the STRING: "0" is non-empty and therefore TRUE, so a guard
// over "0" renders its clause every time. Empty is the only value that switches
// the guard off.
//
// A REQUIRED count must NOT use this — notifyoutbox treats an empty required
// param as missing and rejects the directive at publish time, inside the
// producer's transaction.
func countOrEmpty(n int32) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(int(n))
}

// causesOrEmpty renders Р56·В-54's `remaining_causes` list for a template that
// reads it behind an {{if}} guard.
//
// EMPTY IS THE LOAD-BEARING VALUE, not a degenerate one: an empty list is the
// rule's good case — «Магазин снова доступен покупателям. Предложения вернулись
// в каталог.» may be claimed only then — and a non-empty list is what switches
// the second edition on. `strings.Join(nil, ", ")` is already "", so this
// function exists to SAY that rather than to compute it, and to give the one
// caller a place to change if the vocabulary ever needs mapping.
//
// It joins the raw arm names (SHOP_PAUSED | PRICE_STALE | SHOP_ADMIN_HIDDEN |
// SHOP_VERIFICATION_REVOKED), and the baseline deliberately does NOT print them:
// they are enum literals, not seller-facing copy, and the vault already routes a
// seller to PRT-10's «Почему магазин не виден покупателям» block for the full
// list. A locale that wants to name the cause needs a per-cause phrase, which
// belongs in the translation catalog and not in a Join.
func causesOrEmpty(causes []string) string {
	return strings.Join(causes, ", ")
}

// causePriceStale is the arm name for «прайс старше 14 дней», spelled as the
// producer spells it.
//
// It is `parts.storefront.v1.UnavailableCause`'s arm minus its enum prefix — the
// same four literals `events.parts.v1.ShopVisibilityCause` and the directive's
// own docblock carry, under the MIRROR OBLIGATION stated on the proto field. A
// fifth spelling here would be a drift bug; if this constant ever stops matching,
// the guard silently stops firing and the seller silently stops being told what
// to do, which is why it is a named constant and not an inline string.
const causePriceStale = "PRICE_STALE"

// causePresent reports whether `want` is among the remaining causes, in the
// ""/"1" shape the OPTIONAL param tier requires — empty is the only value that
// switches an {{if}} guard off, exactly as countOrEmpty explains for counts.
//
// WHY A SEPARATE PARAM RATHER THAN A CLEVERER TEMPLATE. Р56·В-53 does not ask for
// «mention that a cause remains»; it asks for one specific sentence — «Предложения
// вернутся после загрузки свежего прайса — текущий устарел» — and gives it a
// purpose: «иначе продавец ждёт возврата витрины, которого не будет». That
// sentence is an INSTRUCTION, and a generic «another cause remains» is not one.
//
// ⚠ AND DO NOT REPLACE THIS WITH `{{if eq .remaining_causes "PRICE_STALE"}}`. It
// renders the tail when price age is the ONLY remaining cause and falls back to
// the generic sentence the moment the shop is also paused — so it passes every
// single-cause test and fails exactly the multi-cause case the rule exists for.
//
// MEMBERSHIP, NOT EXCLUSIVITY, is the reading: В-53 conditions on «прайс уже
// старше 14 дней» and says nothing about other causes. When another cause does
// coexist the seller still gets the full list from PRT-10's «Почему магазин не
// виден покупателям» block, which Р56·В-54 makes the source of truth.
func causePresent(causes []string, want string) string {
	for _, cause := range causes {
		if cause == want {
			return "1"
		}
	}

	return ""
}

// flagWhen renders the ""/"1" flag an OPTIONAL param must carry for an {{if}}
// guard, from a wire enum that picks between editions of one sentence.
//
// It is causePresent's single-valued twin — same shape, same reason ("" is the
// only value that switches a guard off), different question: causePresent asks
// «is this cause among the remaining ones?», flagWhen asks «is the field exactly
// this value?».
//
// USE IT IN PAIRS. Every caller here derives a flag for BOTH arms of a two-valued
// enum instead of one flag and an {{else}}, so that an empty or unrecognised
// value lights neither branch. That turns a producer bug into a missing clause
// rather than a false statement — the buyer of a PICKUP order is never told his
// order was dispatched, and a complaint with no recorded outcome is never
// reported as resolved in the complainant's favour.
func flagWhen(got, want string) string {
	if got == want {
		return "1"
	}

	return ""
}

// The wire vocabularies flagWhen compares against, spelled as directives.proto
// documents them on each field. They are constants for the same reason
// causePriceStale is: a fifth spelling is a silent drift bug — the guard simply
// stops firing and the reader silently stops being told the thing the rule
// exists to tell him.
const (
	// SendPartsOrderConfirmed / …FulfilmentOverdue* `fulfilment_kind` (М-23).
	fulfilmentPickup  = "PICKUP"
	fulfilmentCarrier = "CARRIER"
	// SendPartsOrderConfirmed `deadline_basis` (Р37, Р39-№1). CALENDAR is the
	// other arm and needs no constant: it is the one with a date, and the date
	// itself is what the template reads.
	deadlineFromPayment = "FROM_PAYMENT"
	// SendPartsOrderConfirmedPartially `partial_kind` (Р56·В-56).
	partialPositionsRemoved = "POSITIONS_REMOVED"
	partialQuantityReduced  = "QUANTITY_REDUCED"
	// SendPartsReviewComplaintResolved `outcome` (Р56·В-66).
	complaintOutcomeHidden      = "HIDDEN"
	complaintOutcomeNoViolation = "NO_VIOLATION"
)
