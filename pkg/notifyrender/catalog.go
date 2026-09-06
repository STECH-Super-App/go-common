package notifyrender

import (
	"strings"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// typeKey maps NotificationType to the TOML section name used in the
// translation files (lowercased without the NOTIFICATION_TYPE_ prefix).
var typeKey = map[notificationv1.NotificationType]string{
	notificationv1.NotificationType_NOTIFICATION_TYPE_CHAT_MESSAGE:                        "chat_message",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED:                    "listing_approved",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED:                    "listing_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_UNPUBLISHED:                 "listing_unpublished",
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_PRICE_CHANGED:              "favorite_price_changed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_FAVORITE_LISTING_REMOVED:            "favorite_listing_removed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_MANAGER:  "tenant_invite_organisation_manager",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_OPERATOR: "tenant_invite_organisation_operator",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_MANAGER:      "tenant_invite_personal_manager",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_OPERATOR:     "tenant_invite_personal_operator",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_VERIFIED:               "organisation_verified",
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_ASSIGNED:                   "operator_assigned",
	notificationv1.NotificationType_NOTIFICATION_TYPE_OPERATOR_RELEASED:                   "operator_released",
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_REQUESTED:          "wallet_operation_requested",
	notificationv1.NotificationType_NOTIFICATION_TYPE_WALLET_OPERATION_DECIDED:            "wallet_operation_decided",
	// organisation + tenant lifecycle (slice 2): EMAIL-only flows now also render IN_APP.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_REJECTED:     "organisation_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_ACCEPTED:           "invite_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED:           "invite_declined",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_NEW: "org_admin_transferred_new",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_OLD: "org_admin_transferred_old",
	// tenant membership lifecycle (slice 4): block / unblock / remove / leave.
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED:               "member_blocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED:             "member_unblocked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED:               "member_removed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_REMOVED_ADMIN:  "tenant_member_removed_admin",
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_LEFT:           "tenant_member_left",
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
	// organisation admin-transfer lifecycle (handoff): in-app + email + push.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_INITIATED: "org_admin_transfer_initiated",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_ACCEPTED:  "org_admin_transfer_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_REJECTED:  "org_admin_transfer_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_CANCELLED: "org_admin_transfer_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_EXPIRED:   "org_admin_transfer_expired",
	// delivery lifecycle (order-service delivery vertical, gen-go-lib NotificationType 47-64).
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED:         "delivery_request_created",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_ACCEPTED:        "delivery_request_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_REJECTED:        "delivery_request_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_SENT:      "delivery_counter_offer_sent",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_ACCEPTED:  "delivery_counter_offer_accepted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_DECLINED:  "delivery_counter_offer_declined",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_WITHDRAWN: "delivery_counter_offer_withdrawn",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED:       "delivery_request_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY:           "delivery_loading_today",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED:         "delivery_request_expired",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_IN_TRANSIT:              "delivery_in_transit",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AWAITING_RECEIPT:        "delivery_awaiting_receipt",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_RECEIPT_REMINDER:        "delivery_receipt_reminder",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AUTO_CONFIRMED:          "delivery_auto_confirmed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_COMPLETED:       "delivery_request_completed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_INVITE:           "delivery_review_invite",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_WINDOW_ENDING:    "delivery_review_window_ending",
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED:       "delivery_cascade_cancelled",
	// organisation change-request moderation (NotificationType 65-68): the three
	// submitter-facing outcomes plus the admin-facing contacts-changed notice.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_APPROVED:            "organisation_change_approved",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_REJECTED:            "organisation_change_rejected",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_DOCUMENTS_REQUESTED: "organisation_change_documents_requested",
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CONTACTS_CHANGED:           "organisation_contacts_changed",
	// organisation deactivated after its leading user deleted their account
	// (П-14). Tenant-addressed fan-out to the org's ADMIN + MANAGER members.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DEACTIVATED: "organisation_deactivated",

	// ─── Маркетплейс запчастей (parts) ───
	//
	// A type absent from this map CANNOT RENDER: ExtractParams' default arm
	// returns ErrUnknownType, and inbox-service wraps that error raw
	// (internal/application/ingestion/service.go) with no retry tier wired — so a
	// parts directive of an unmapped type is dead-lettered onto
	// notification.events.dlq.inbox on FIRST delivery. That was the live state of
	// every parts directive until these entries landed, reproduced in
	// parts_liveproof_test.go.
	//
	// Sixty-three SendParts* directives are declared in directives.proto. The
	// twelve below are the ones a producer can actually emit: the TEN written by
	// sale-service's PartsNotificationDirectiveBuilder (eight from the storefront
	// clock and the admin sanction pair; two of the ten — the shop-level lifts —
	// are built and unit-tested with their route still pending), plus the price-file
	// importer's PAIR, which has no producer yet and is here because its patch was
	// already written and verified. The other 51 land the same way, one at a time,
	// when their producer does.
	//
	// NO PARTS TEMPLATE NAMES THE SHOP. OWNER-ANSWERS-2026-08-31 B-5 removed
	// `shop_name` from all 53 SendParts* messages (reserved, not blanked): М-01
	// makes the shop's name the COMPANY's name, and every payload already carries
	// the tenant the client resolves it from. A push has no client to hydrate it,
	// and the owner ruled removal knowing that — so these strings address the
	// recipient's own shop as «your shop» rather than re-adding a field.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_HIDDEN_BY_ADMIN:          "parts_offer_hidden_by_admin",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_SANCTION_LIFTED:          "parts_offer_sanction_lifted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED:           "parts_shop_sanction_lifted",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED:     "parts_shop_verification_restored",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK:            "parts_offer_back_in_stock",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED:         "parts_favorite_price_dropped",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_OFFER_APPEARED:    "parts_subscription_offer_appeared",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_EXPIRING:          "parts_subscription_expiring",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_STALE_WARNING:       "parts_price_list_stale_warning",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFERS_HIDDEN_PRICE_LIST_STALE: "parts_offers_hidden_price_list_stale",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED:           "parts_price_list_processed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_FILE_FAILED:         "parts_price_list_file_failed",

	// The FORTY-NINE below are D-16a: every remaining parts type whose Russian
	// the vault already carries, transcribed from «Уведомления запчастей.md» and
	// composed into English here. They have no producer yet — order-service's
	// PARTS vertical and sale-service's PartsDemand are both unbuilt — and that
	// ordering is deliberate: D-9's gate is templates BEFORE producers, because
	// an unmapped type does not degrade, it dead-letters on first delivery and
	// fails the transaction that published it.
	//
	// TWO of the sixty-three are still absent, both for the same reason — the
	// vault carries no text for them in ANY of its five text tables:
	//   * PARTS_ORDER_CONTACT_HANDOVER (123, Р47) — has a recipient, a target
	//     screen and a «не отключается» flag, and no sentence. OWNER-ANSWERS
	//     2026-09-02 D-8 says the same. One sentence is owed.
	//   * PARTS_SHOP_VERIFICATION_REVOKED (124, Р51) — the only one of the four
	//     shop-level texts whose Russian was never written; the other three are
	//     verbatim in the vault and are below.
	// Composing English from the owner's Russian is the fleet convention.
	// Composing the RUSSIAN would be inventing product copy, so neither is here.

	// ── Р40 matching queue: the three seller directives the админка's «Очередь
	// сопоставления» fires (71–73). Counted, never named: the reasons stay in the
	// PRT-12 report and the push carries only how many rows moved.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_MATCHED:         "parts_positions_matched",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_PUBLISHED_RETRO: "parts_positions_published_retro",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_REJECTED:        "parts_positions_rejected",
	// ── М-20 shop-level sanction and the Р40 badge (78, 80), plus Р56·В-52's
	// buyer-facing notice (126). The two seller arms address «your shop»; the
	// buyer arm keys on the buyer's own order number, which is the half B-5 left
	// standing (see the proto's own note on that message).
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_HIDDEN_BY_ADMIN:          "parts_shop_hidden_by_admin",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFIED_BADGE_REVOKED:   "parts_shop_verified_badge_revoked",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_UNAVAILABLE_ORDER_NOTICE: "parts_shop_unavailable_order_notice",
	// ── The order lifecycle (81–102), which order-service emits off the PARTS
	// order aggregate. Every one of them carries `order_no` and every one of them
	// declares it REQUIRED — the delivery vertical's `request_no` is optional only
	// because directives emitted before numbering shipped carry it empty, and
	// parts has no such legacy: it has no producer at all yet. After B-5 the
	// number is also the ONLY identifier left in the sentence, so a push without
	// one cannot be acted on when a buyer holds several live orders.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CREATED:                   "parts_order_created",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED:                 "parts_order_confirmed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY:       "parts_order_confirmed_partially",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_SELLER: "parts_order_fulfilment_overdue_seller",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_BUYER:  "parts_order_fulfilment_overdue_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_DISPATCH_REMINDER:         "parts_order_dispatch_reminder",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_READY_FOR_PICKUP:          "parts_order_ready_for_pickup",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_HANDED_TO_CARRIER:         "parts_order_handed_to_carrier",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_PICKED_UP:                 "parts_order_picked_up",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RECEIPT_REMINDER:          "parts_order_receipt_reminder",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RECEIPT_CONFIRMED:         "parts_order_receipt_confirmed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_AUTO_CONFIRMED_BUYER:      "parts_order_auto_confirmed_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_AUTO_CONFIRMED_SELLER:     "parts_order_auto_confirmed_seller",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_REJECTED_BY_SELLER:        "parts_order_rejected_by_seller",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_BUYER:        "parts_order_cancelled_by_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_SELLER:       "parts_order_cancelled_by_seller",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RESPONSE_REMINDER:         "parts_order_response_reminder",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_BUYER:             "parts_order_expired_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_SELLER:            "parts_order_expired_seller",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_REPORTED:            "parts_order_issue_reported",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_RESOLVED_BUYER:      "parts_order_issue_resolved_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_RESOLVED_SELLER:     "parts_order_issue_resolved_seller",
	// ── Подбор (103–114) plus Р56's two quote-withdrawal arms (127, 128). Same
	// reasoning for `request_no`. SOURCING_REQUEST_CREATED is the exception that
	// proves it: the vault's own text for that row names no number, so the param
	// is declared OPTIONAL and left out of the baseline — a translation may still
	// use it, which is why it is declared at all.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CREATED:               "parts_sourcing_request_created",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_RECEIVED:                "parts_sourcing_quote_received",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_NO_QUOTES_YET:                 "parts_sourcing_no_quotes_yet",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_ALL_SHOPS_DECLINED:            "parts_sourcing_all_shops_declined",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_BUYER:       "parts_sourcing_request_closed_by_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_BUYER: "parts_sourcing_request_closed_by_order_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_SHOP:  "parts_sourcing_request_closed_by_order_shop",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CANCELLED:             "parts_sourcing_request_cancelled",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRING:              "parts_sourcing_request_expiring",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXTENDED:              "parts_sourcing_request_extended",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_BUYER:         "parts_sourcing_request_expired_buyer",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_SHOP:          "parts_sourcing_request_expired_shop",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_SHOP_CLOSED:   "parts_sourcing_quote_withdrawn_shop_closed",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_BY_SHOP:       "parts_sourcing_quote_withdrawn_by_shop",
	// ── Р42 catalogue-addition decisions (115, 116). The addition may be PARTIAL
	// (type only, brand only, brand without model), so all three machinery fields
	// are optional and the texts branch on which of them arrived.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_ADDED:    "parts_catalogue_machinery_added",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_REJECTED: "parts_catalogue_machinery_rejected",
	// ── Reviews (117–120) and Р56·В-66's complaint outcome (131). REVIEW_INVITE is
	// the second of the two parts rows the matrix also marks EMAIL.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_INVITE:              "parts_review_invite",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_REMINDER:            "parts_review_reminder",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_RECEIVED:            "parts_review_received",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_HIDDEN_BY_COMPLAINT: "parts_review_hidden_by_complaint",
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED:  "parts_review_complaint_resolved",
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
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_MANAGER: {
		"tenant_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_OPERATOR: {
		"tenant_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_MANAGER: {
		"tenant_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_OPERATOR: {
		"tenant_name", "inviter_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_VERIFIED: {
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
	// ─── organisation + tenant lifecycle (slice 2) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_REJECTED: {
		"reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_ACCEPTED: {
		"phone", "role",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_INVITE_DECLINED: {
		"phone",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_NEW: {
		"tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_OLD: {
		"tenant_name",
	},
	// ─── tenant membership lifecycle (slice 4) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED: {
		"tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED: {
		"tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED: {
		"tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_REMOVED_ADMIN: {
		"tenant_name", "removed_member_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_LEFT: {
		"tenant_name", "member_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_MANAGER: {
		"tenant_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_OPERATOR: {
		"tenant_name",
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
	// ─── organisation admin-transfer lifecycle (handoff) ───
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_INITIATED: {
		"organization_name", "from_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_ACCEPTED: {
		"organization_name", "to_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_REJECTED: {
		"organization_name", "to_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_CANCELLED: {
		"organization_name", "from_user_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFER_EXPIRED: {
		"organization_name", "counterparty_name",
	},
	// ─── delivery lifecycle (order-service delivery vertical) ───
	// Only the three delivery types that interpolate a REQUIRED param are listed;
	// the other 15 need no entry (the required-param loop and the baseline
	// reverse-check both treat a nil entry as "no required params"). The
	// human-facing request number rides on 9 of the 18 delivery payloads and is
	// declared in optionalParams instead — see the note there for why it must not
	// be a required param.
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_ACCEPTED: {
		// final_price holds a DISPLAY-formatted money string, exactly as
		// amount / old_price do today: notifyrender interpolates strings only.
		// The emitter pre-formats the price and ships it as the payload's
		// string final_price field; ExtractParams passes it through verbatim.
		"final_price", "currency",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED: {
		// cancel_reason is optional (late-cancellation only), so — mirroring
		// admin_transfer_rejected's reason — it is neither templated nor declared.
		"cancelled_by",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY: {
		"route",
	},
	// ─── organisation change-request moderation ───
	// `comment` is deliberately absent from every entry below: the moderator
	// comment is optional, and notifyoutbox treats an empty required param as
	// missing — declaring it would fail the render whenever it is left blank.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_APPROVED: {
		"organization_name", "submitted_at",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_REJECTED: {
		"organization_name", "submitted_at", "reasons",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CHANGE_DOCUMENTS_REQUESTED: {
		"organization_name", "submitted_at", "reasons",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_CONTACTS_CHANGED: {
		"organization_name",
	},
	// ─── organisation deactivated (П-14, account deletion) ───
	// organisation_name only. The payload also carries organisation_id, but that
	// is a routing id with no template use — the same treatment
	// organisation_contacts_changed gives its own id. The name is denormalized
	// onto the directive precisely so this row renders without a read-path
	// lookup to an organisation that is now INACTIVE.
	notificationv1.NotificationType_NOTIFICATION_TYPE_ORGANISATION_DEACTIVATED: {
		"organization_name",
	},

	// ─── Маркетплейс запчастей (parts) ───
	//
	// WHAT IS REQUIRED HERE IS DECIDED BY THE PHP PRODUCER, NOT BY THE COPY.
	// PartsNotificationDirectiveBuilder passes `$productName ?? ''` and
	// `$price === null ? '' : …` — an unmatched позиция has no card to take a name
	// from and a позиция with no ladder rung has no price — so every one of those
	// is OPTIONAL below and guarded in the baseline. Declaring one required would
	// make notifyoutbox reject the directive at publish time, inside the very
	// transaction the sweep is reporting on.
	//
	// Int32 counts go the other way. ExtractParams formats them with strconv.Itoa,
	// so a zero arrives as the non-empty string "0" — it renders, and it satisfies
	// the empty-means-missing check — which is why every count that always has a
	// meaning is required. The one exception is new_address_count; see optionalParams.
	//
	// The admin's `reason` is required by Р40-№15, which makes the text mandatory
	// on the sanction itself, so it can never arrive blank.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_HIDDEN_BY_ADMIN: {
		"reason",
	},
	// Nothing required: a lift is fully described by the позиция being visible
	// again, and the only variable — its name — may legitimately be absent.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_SANCTION_LIFTED: nil,
	// Р56·В-54's conditional lift text keys entirely on `remaining_causes`, which
	// is EMPTY in the good case, so it cannot be required either.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED:       nil,
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED: nil,
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK:        nil,
	// Both prices are non-nullable ints in the builder — Р43 measures a fall
	// between two known numbers, so neither can be absent.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED: {
		"old_price", "new_price",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_OFFER_APPEARED: nil,
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_EXPIRING: {
		"days_left",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_STALE_WARNING: {
		"days_since_upload",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFERS_HIDDEN_PRICE_LIST_STALE: {
		"days_since_upload",
	},
	// Р31's three counters. new_address_count is deliberately NOT required: the
	// row gets a SECOND EDITION only when the file introduced new shipping
	// addresses, so it lives in optionalParams.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED: {
		"published_count", "matching_count", "error_count",
	},
	// No counters: the whole file was rejected and nothing was published, so the
	// cause lives in the PRT-12 import report rather than in row counts.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_FILE_FAILED: {
		"file_name",
	},

	// ─── D-16a: the order, sourcing, catalogue, review and shop-level families ───
	//
	// Same two tiers as the twelve above, and the same rule for counts: a REQUIRED
	// count goes through strconv.Itoa (a zero renders as "0", which is non-empty
	// and therefore satisfies notifyoutbox's required-param check), an OPTIONAL one
	// through countOrEmpty. Every count here is required — none of these rows fires
	// with a zero that means anything.
	//
	// An ENUM the copy branches on is never a param. `fulfilment_kind`,
	// `deadline_basis`, `partial_kind` and the complaint `outcome` are read in
	// ExtractParams and turned into PAIRED "1"/"" flags, exactly as `O-67` ruled for
	// `remaining_price_stale`. Two flags rather than one plus an {{else}} is the
	// whole point: an absent or unrecognised enum then lights NEITHER branch, so the
	// sentence loses a clause instead of asserting the wrong one — a pickup buyer is
	// never told his order will be dispatched. Flags are optional by construction
	// ("" is a legitimate value and a required empty is a publish-time rejection),
	// which is why the raw enum keeps no required tier of its own.
	// ── Р40 matching queue: the three seller directives the админка's «Очередь
	// сопоставления» fires (71–73). Counted, never named: the reasons stay in the
	// PRT-12 report and the push carries only how many rows moved.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_MATCHED: {
		"published_count",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_PUBLISHED_RETRO: {
		"published_count",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_POSITIONS_REJECTED: {
		"rejected_count",
	},
	// ── М-20 shop-level sanction and the Р40 badge (78, 80), plus Р56·В-52's
	// buyer-facing notice (126). The two seller arms address «your shop»; the
	// buyer arm keys on the buyer's own order number, which is the half B-5 left
	// standing (see the proto's own note on that message).
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_HIDDEN_BY_ADMIN: {
		"reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_UNAVAILABLE_ORDER_NOTICE: {
		"order_no",
	},
	// ── The order lifecycle (81–102), which order-service emits off the PARTS
	// order aggregate. Every one of them carries `order_no` and every one of them
	// declares it REQUIRED — the delivery vertical's `request_no` is optional only
	// because directives emitted before numbering shipped carry it empty, and
	// parts has no such legacy: it has no producer at all yet. After B-5 the
	// number is also the ONLY identifier left in the sentence, so a push without
	// one cannot be acted on when a buyer holds several live orders.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CREATED: {
		"order_no", "position_count", "total",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY: {
		"order_no", "confirmed_count", "total_count",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_SELLER: {
		"order_no", "deadline_date",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_BUYER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_DISPATCH_REMINDER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_READY_FOR_PICKUP: {
		"order_no", "pickup_address",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_HANDED_TO_CARRIER: {
		"order_no", "carrier_name",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_PICKED_UP: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RECEIPT_REMINDER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RECEIPT_CONFIRMED: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_AUTO_CONFIRMED_BUYER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_AUTO_CONFIRMED_SELLER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_REJECTED_BY_SELLER: {
		"order_no", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_BUYER: {
		"order_no", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CANCELLED_BY_SELLER: {
		"order_no", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_RESPONSE_REMINDER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_BUYER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_EXPIRED_SELLER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_REPORTED: {
		"order_no", "reason",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_RESOLVED_BUYER: {
		"order_no", "outcome",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_ISSUE_RESOLVED_SELLER: {
		"order_no", "outcome",
	},
	// ── Подбор (103–114) plus Р56's two quote-withdrawal arms (127, 128). Same
	// reasoning for `request_no`. SOURCING_REQUEST_CREATED is the exception that
	// proves it: the vault's own text for that row names no number, so the param
	// is declared OPTIONAL and left out of the baseline — a translation may still
	// use it, which is why it is declared at all.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CREATED: {
		"machinery_type", "brand",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_RECEIVED: {
		"request_no", "position_count", "total",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_NO_QUOTES_YET: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_ALL_SHOPS_DECLINED: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_BUYER: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_BUYER: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CLOSED_BY_ORDER_SHOP: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CANCELLED: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRING: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXTENDED: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_BUYER: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_EXPIRED_SHOP: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_SHOP_CLOSED: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_QUOTE_WITHDRAWN_BY_SHOP: {
		"request_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_REJECTED: {
		"reason",
	},
	// ── Reviews (117–120) and Р56·В-66's complaint outcome (131). REVIEW_INVITE is
	// the second of the two parts rows the matrix also marks EMAIL.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_INVITE: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_REMINDER: {
		"order_no",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_RECEIVED: {
		"rating",
	},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_HIDDEN_BY_COMPLAINT: {
		"reason",
	},
}

// optionalParams declares params that MAY arrive empty (or absent) and are
// therefore templated behind a conditional. They are a second, weaker tier of
// the same param contract and differ from requiredParams in three ways:
//
//   - notifyoutbox's producer-side validator only walks RequiredParams, so an
//     empty optional value does NOT reject the directive at publish time
//     (for a required param, empty means missing).
//   - Render fills an absent optional key with "" before templating, so the
//     baseline's {{if}} guard cannot trip missingkey=error.
//   - The baseline MUST reference an optional param behind that guard, so an
//     empty value renders the plain sentence and never a dangling "#".
//
// request_no — the human-facing delivery request number (Д-13, «Заявка #12345»)
// — is the first of these. order-service stamps it into 9 of the 18 SendDelivery*
// payloads, but directives emitted before numbering shipped carry it empty, and
// a delivery request may be notified about before a number is assigned. Those
// must still render, minus the number.
var optionalParams = map[notificationv1.NotificationType][]string{
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED:      {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED:    {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY:        {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED:      {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_RECEIPT_REMINDER:     {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AUTO_CONFIRMED:       {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_INVITE:        {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_WINDOW_ENDING: {"request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED:    {"request_no"},

	// ─── parts ───
	//
	// `product_name` is optional on every parts row that carries one, and that is
	// a PROTO-LEVEL fact rather than defensive coding: a позиция the administrator
	// has not yet matched to a card has no name anywhere in the storefront — the
	// file's «Наименование» cell is stored on no table — so the builder sends
	// `''` and protojson omits it. The alternative the builder rejects, in its own
	// words, is «echoing the артикул as if it were a name the seller wrote».
	//
	// `price` / `price_from` are optional for the same reason one step further
	// down: a позиция whose ladder has no rung has no price to quote.
	//
	// `remaining_causes` is Р56·В-54's whole mechanism. EMPTY means the storefront
	// really is back, which is the only case the first-edition text may claim; a
	// non-empty list means another cause still holds it dark. It can therefore
	// never be required — the good case is the empty one.
	//
	// `remaining_price_stale` rides beside it and is DERIVED FROM THE SAME FIELD
	// (`O-67`, owner ruling of 01.09.2026, option (a)): the wire sends a list, the
	// flattening to one string happens in this package, and a flat string cannot
	// answer «is price age among them?». It carries Р56·В-53's SECOND EDITION —
	// the “upload a fresh price list” instruction — which the generic branch
	// cannot express. Both are optional and both are read only behind an {{if}}.
	//
	// `new_address_count` carries Р31's SECOND EDITION of «Прайс обработан»
	// («Новых адресов: N — проверьте настройку самовывоза»), owed only when the
	// file introduced one. ExtractParams must emit "" and not "0" for a zero
	// count: Go's template truth test runs on the STRING, so "0" is non-empty and
	// therefore TRUE, and an {{if .new_address_count}} guard over "0" would append
	// the second edition to every import. Empty is the only value that switches a
	// guard off — which is why countOrEmpty exists and why a REQUIRED count must
	// never use it.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_HIDDEN_BY_ADMIN:       {"product_name"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_SANCTION_LIFTED:       {"product_name"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_SANCTION_LIFTED:        {"remaining_causes", "remaining_price_stale"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SHOP_VERIFICATION_RESTORED:  {"remaining_causes", "remaining_price_stale"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_OFFER_BACK_IN_STOCK:         {"product_name", "price"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_FAVORITE_PRICE_DROPPED:      {"product_name"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_OFFER_APPEARED: {"product_name", "price_from"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SUBSCRIPTION_EXPIRING:       {"product_name"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_PRICE_LIST_PROCESSED:        {"new_address_count"},

	// ─── D-16a ───
	//
	// The paired branch flags described in requiredParams live here, together with
	// the ordinary may-be-empty strings: `product_name` on an order with more than
	// one position, `sourcing_request_no` on a catalogue order that came from no
	// подбор, `tracking_number` before М-14's «настоятельно рекомендован» track is
	// supplied, `ready_date` on Р37's «от оплаты» terms, `model` on a Р42 request
	// that named none, and `request_no` on SOURCING_REQUEST_CREATED, whose vault
	// text names no number — declared so a translation MAY use it, and left out of
	// the baseline because the owner's sentence leaves it out.
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CREATED:                   {"product_name", "sourcing_request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED:                 {"ready_date", "is_pickup", "is_carrier", "is_from_payment"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_CONFIRMED_PARTIALLY:       {"is_positions_removed", "is_quantity_reduced"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_SELLER: {"is_pickup", "is_carrier"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_FULFILMENT_OVERDUE_BUYER:  {"is_pickup", "is_carrier"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_ORDER_HANDED_TO_CARRIER:         {"tracking_number"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CREATED:        {"model", "request_no"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_SOURCING_REQUEST_CANCELLED:      {"reason"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_ADDED:       {"machinery_type", "brand", "model"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_CATALOGUE_MACHINERY_REJECTED:    {"machinery_type", "brand", "model"},
	notificationv1.NotificationType_NOTIFICATION_TYPE_PARTS_REVIEW_COMPLAINT_RESOLVED:       {"outcome_hidden", "outcome_no_violation"},
}

// RequiredParams returns the param names required for type t — the contract
// producers are held to (an empty value is rejected at publish time). Returns
// nil for unknown / reserved types.
func RequiredParams(t notificationv1.NotificationType) []string {
	return requiredParams[t]
}

// OptionalParams returns the param names type t may render but may also leave
// empty. Returns nil for types with no optional params.
func OptionalParams(t notificationv1.NotificationType) []string {
	return optionalParams[t]
}

// allowedParams returns the full set of params a template for type t may
// reference: the required ones plus the optional ones. Always a fresh slice —
// callers must not observe (or mutate) the catalog's backing arrays.
func allowedParams(t notificationv1.NotificationType) []string {
	req, opt := requiredParams[t], optionalParams[t]
	out := make([]string, 0, len(req)+len(opt))
	out = append(out, req...)
	return append(out, opt...)
}

// sectionToType is the reverse of typeKey: catalog section name →
// NotificationType. Built once at package init.
var sectionToType = func() map[string]notificationv1.NotificationType {
	m := make(map[string]notificationv1.NotificationType, len(typeKey))
	for t, name := range typeKey {
		m[name] = t
	}
	return m
}()

// AllowedParamsByKey maps a dotted catalog key ("<section>.title" or
// "<section>.body") to the param names declared for that section's
// NotificationType — required AND optional, since a translation may legitimately
// reference either. It is the reverse of typeKey plus a suffix strip.
//
// Returns (nil, false) for a key without a .title/.body suffix or one whose
// section maps to no known NotificationType. Task 5 wires this into
// i18n.WithAllowedParams so overlay translations may reference only declared
// placeholders.
func AllowedParamsByKey(key string) ([]string, bool) {
	var section string
	switch {
	case strings.HasSuffix(key, ".title"):
		section = strings.TrimSuffix(key, ".title")
	case strings.HasSuffix(key, ".body"):
		section = strings.TrimSuffix(key, ".body")
	default:
		return nil, false
	}
	t, ok := sectionToType[section]
	if !ok {
		return nil, false
	}
	return allowedParams(t), true
}
