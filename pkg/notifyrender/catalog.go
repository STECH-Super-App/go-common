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
	// team membership lifecycle (slice 4): block / unblock / remove.
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED:            "member_blocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED:          "member_unblocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED:            "member_removed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN: "team_member_removed_admin",
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
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_MANAGER: {
		"team_name", "inviter_name", "tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_INVITE_TENANT_OPERATOR: {
		"team_name", "inviter_name", "tenant_name",
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
}

// RequiredParams returns the param names required for type t. Returns
// nil for unknown / reserved types.
func RequiredParams(t notificationv1.NotificationType) []string {
	return requiredParams[t]
}
