package notifyrender

import (
	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// typeKey maps NotificationType to the TOML section name used in the
// translation files (lowercased without the NOTIFICATION_TYPE_ prefix).
var typeKey = map[notificationv1.NotificationType]string{
	notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE:                "chat_message",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED:            "listing_approved",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED:            "listing_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED:         "listing_unpublished",
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_PRICE_CHANGED:      "favorite_price_changed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_LISTING_REMOVED:    "favorite_listing_removed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_MANAGER:  "team_invite_tenant_manager",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_OPERATOR: "team_invite_tenant_operator",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_USER_MANAGER:    "team_invite_user_manager",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_USER_OPERATOR:   "team_invite_user_operator",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_VERIFIED:             "tenant_verified",
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_ASSIGNED:           "operator_assigned",
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_RELEASED:           "operator_released",
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_REQUESTED:  "wallet_operation_requested",
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_DECIDED:    "wallet_operation_decided",
	// tenant + team lifecycle (slice 2): EMAIL-only flows now also render IN_APP.
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_REJECTED:       "tenant_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_ACCEPTED:       "invite_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED:       "invite_declined",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_NEW: "admin_transferred_new",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_OLD: "admin_transferred_old",
	// team membership lifecycle (slice 4): block / unblock / remove / leave.
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED:               "member_blocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED:             "member_unblocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED:               "member_removed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN:    "team_member_removed_admin",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_LEFT:             "team_member_left",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_MANAGER:  "member_role_changed_manager",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_OPERATOR: "member_role_changed_operator",
	// order lifecycle (order-service contracts, gen-go-lib NotificationType 27-38).
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_CREATED:         "order_request_created",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_ACCEPTED:        "order_request_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TERMS_AGREED:            "order_terms_agreed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CONFIRMED:               "order_confirmed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_SENT:      "order_counter_offer_sent",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_WITHDRAWN: "order_counter_offer_withdrawn",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED:               "order_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_CANCELLED:          "order_auto_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TRANSFERRED:             "order_transferred",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_RECEIPT_CONFIRMED:       "order_receipt_confirmed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_COMPLETED:          "order_auto_completed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_WINDOW_ENDING:    "order_review_window_ending",
	// tenant admin-transfer lifecycle (handoff): in-app + email + push.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_INITIATED: "admin_transfer_initiated",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_ACCEPTED:  "admin_transfer_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_REJECTED:  "admin_transfer_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_CANCELLED: "admin_transfer_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_EXPIRED:   "admin_transfer_expired",
	// SYSTEM is reserved; not in the catalog.
	// PLATFORM_MESSAGE (slice 5) is verbatim free-text; deliberately NOT in the
	// catalog — it has no template. See notifyrender.IsVerbatim / RenderVerbatim.
}

// requiredParams is the single source of truth for the template
// param contract per NotificationType. A unit test asserts:
//   - Every TOML {{.field}} placeholder is in this list.
//   - Every field in this list appears in at least one TOML placeholder.
//   - ExtractParams returns exactly these field names.
var requiredParams = map[notificationv1.NotificationType][]string{
	notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE: {
		"sender_name", "preview",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED: {
		"listing_title", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED: {
		"listing_title", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_PRICE_CHANGED: {
		"listing_title", "old_price", "new_price", "currency",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_LISTING_REMOVED: {
		"listing_title",
	},
	// No directive payload carries a tenant/org name for team invites
	// (SendInviteExistingUser has team_name + inviter_name + role only), so the
	// TENANT variants require the same params as the USER variants.
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_MANAGER: {
		"team_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_OPERATOR: {
		"team_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_USER_MANAGER: {
		"team_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_USER_OPERATOR: {
		"team_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_VERIFIED: {
		"organization_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_ASSIGNED: {
		"operator_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_RELEASED: {
		"operator_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_REQUESTED: {
		"amount", "currency", "operation_kind",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_DECIDED: {
		"amount", "currency", "decision",
	},
	// ─── tenant + team lifecycle (slice 2) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_REJECTED: {
		"reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_ACCEPTED: {
		"phone", "role",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED: {
		"phone",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_NEW: {
		"team_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFERRED_OLD: {
		"team_name",
	},
	// ─── team membership lifecycle (slice 4) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED: {
		"team_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED: {
		"team_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED: {
		"team_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN: {
		"team_name", "removed_member_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_LEFT: {
		"team_name", "member_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_MANAGER: {
		"team_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_OPERATOR: {
		"team_name",
	},
	// ─── order lifecycle (order-service) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_CREATED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_ACCEPTED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TERMS_AGREED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CONFIRMED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_SENT: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_WITHDRAWN: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED: {
		"listing_title", "cancelled_by",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_CANCELLED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TRANSFERRED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_RECEIPT_CONFIRMED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_COMPLETED: {
		"listing_title",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_WINDOW_ENDING: {
		"listing_title",
	},
	// ─── tenant admin-transfer lifecycle (handoff) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_INITIATED: {
		"organization_name", "from_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_ACCEPTED: {
		"organization_name", "to_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_REJECTED: {
		"organization_name", "to_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_CANCELLED: {
		"organization_name", "from_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_EXPIRED: {
		"organization_name", "counterparty_name",
	},
}

// RequiredParams returns the param names required for type t. Returns
// nil for unknown / reserved types.
func RequiredParams(t notificationv1.NotificationType) []string {
	return requiredParams[t]
}
