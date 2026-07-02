package notifyrender

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/text/language"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

// testRenderer builds a Renderer backed by a minimal in-memory bundle that
// only contains the listing_approved template. Use for error-path tests and
// tests that only need one type resolved.
func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."}
		}`)},
	}
	b, err := i18n.LoadBundle(fsys, language.English)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	return NewRenderer(b)
}

// testRendererFull builds a Renderer with the complete catalog in both en and
// ru locales. Use for tests that iterate typeKey or render multiple types.
func testRendererFull(t *testing.T) *Renderer {
	t.Helper()
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
  "chat_message": {"title": "New message from {{.sender_name}}", "body": "{{.preview}}"},
  "listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."},
  "listing_rejected": {"title": "Listing rejected", "body": "Your listing '{{.listing_title}}' was rejected: {{.reason}}"},
  "listing_unpublished": {"title": "Listing unpublished", "body": "Your listing '{{.listing_title}}' was unpublished: {{.reason}}"},
  "favorite_price_changed": {"title": "Price changed", "body": "The price of '{{.listing_title}}' changed from {{.old_price}} to {{.new_price}} {{.currency}}."},
  "favorite_listing_removed": {"title": "Favorite listing removed", "body": "'{{.listing_title}}' is no longer available."},
  "team_invite_tenant_manager": {"title": "Invitation to join {{.tenant_name}}", "body": "{{.inviter_name}} invited you to join {{.team_name}} as a manager."},
  "team_invite_tenant_operator": {"title": "Invitation to join {{.tenant_name}}", "body": "{{.inviter_name}} invited you to join {{.team_name}} as an operator."},
  "team_invite_user_manager": {"title": "Team invitation", "body": "{{.inviter_name}} invited you to {{.team_name}} as a manager."},
  "team_invite_user_operator": {"title": "Team invitation", "body": "{{.inviter_name}} invited you to {{.team_name}} as an operator."},
  "tenant_verified": {"title": "Organization verified", "body": "{{.organization_name}} has been verified."},
  "operator_assigned": {"title": "Operator assigned", "body": "{{.operator_name}} has been assigned."},
  "operator_released": {"title": "Operator released", "body": "{{.operator_name}} has been released."},
  "wallet_operation_requested": {"title": "Wallet operation requested", "body": "A {{.operation_kind}} of {{.amount}} {{.currency}} has been requested."},
  "wallet_operation_decided": {"title": "Wallet operation {{.decision}}", "body": "Your wallet operation for {{.amount}} {{.currency}} was {{.decision}}."},
  "tenant_rejected": {"title": "Organization rejected", "body": "Your organization was rejected: {{.reason}}"},
  "invite_accepted": {"title": "Invitation accepted", "body": "{{.phone}} accepted your invitation to join as {{.role}}."},
  "invite_declined": {"title": "Invitation declined", "body": "{{.phone}} declined your invitation."},
  "admin_transferred_new": {"title": "You are now the team admin", "body": "You have been made the admin of {{.team_name}}."},
  "admin_transferred_old": {"title": "Team admin transferred", "body": "You transferred admin rights for {{.team_name}}."},
  "member_blocked": {"title": "Access temporarily restricted", "body": "Your access to the team {{.team_name}} has been temporarily restricted."},
  "member_unblocked": {"title": "Access restored", "body": "Your access to the team {{.team_name}} has been restored."},
  "member_removed": {"title": "Removed from team", "body": "You have been removed from the team {{.team_name}}."},
  "team_member_removed_admin": {"title": "{{.removed_member_name}} left the team", "body": "{{.removed_member_name}} is no longer a member of the team {{.team_name}}."},
  "order_request_created": {"title": "New order request", "body": "You have a new request for '{{.listing_title}}'."},
  "order_request_accepted": {"title": "Request accepted", "body": "Your request for '{{.listing_title}}' was accepted."},
  "order_terms_agreed": {"title": "Terms agreed", "body": "Both sides agreed on terms for '{{.listing_title}}'."},
  "order_confirmed": {"title": "Order confirmed", "body": "The order for '{{.listing_title}}' is confirmed."},
  "order_counter_offer_sent": {"title": "Counter offer received", "body": "A counter offer was sent for '{{.listing_title}}'."},
  "order_counter_offer_withdrawn": {"title": "Counter offer withdrawn", "body": "The counter offer for '{{.listing_title}}' was withdrawn."},
  "order_cancelled": {"title": "Order cancelled", "body": "The order for '{{.listing_title}}' was cancelled by {{.cancelled_by}}."},
  "order_auto_cancelled": {"title": "Order auto-cancelled", "body": "The order for '{{.listing_title}}' was automatically cancelled."},
  "order_transferred": {"title": "Order transferred", "body": "The order for '{{.listing_title}}' was transferred."},
  "order_receipt_confirmed": {"title": "Receipt confirmed", "body": "Receipt was confirmed for '{{.listing_title}}'."},
  "order_auto_completed": {"title": "Order auto-completed", "body": "The order for '{{.listing_title}}' was automatically completed."},
  "order_review_window_ending": {"title": "Review window ending", "body": "The review window for '{{.listing_title}}' is ending soon."}
}`)},
		"ru.json": {Data: []byte(`{
  "chat_message": {"title": "Новое сообщение от {{.sender_name}}", "body": "{{.preview}}"},
  "listing_approved": {"title": "Объявление одобрено", "body": "Ваше объявление «{{.listing_title}}» опубликовано."},
  "listing_rejected": {"title": "Объявление отклонено", "body": "Ваше объявление «{{.listing_title}}» отклонено: {{.reason}}"},
  "listing_unpublished": {"title": "Объявление снято с публикации", "body": "Ваше объявление «{{.listing_title}}» снято с публикации: {{.reason}}"},
  "favorite_price_changed": {"title": "Цена изменилась", "body": "Цена «{{.listing_title}}» изменилась с {{.old_price}} на {{.new_price}} {{.currency}}."},
  "favorite_listing_removed": {"title": "Избранное объявление удалено", "body": "«{{.listing_title}}» больше не доступно."},
  "team_invite_tenant_manager": {"title": "Приглашение в {{.tenant_name}}", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли менеджера."},
  "team_invite_tenant_operator": {"title": "Приглашение в {{.tenant_name}}", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли оператора."},
  "team_invite_user_manager": {"title": "Приглашение в команду", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли менеджера."},
  "team_invite_user_operator": {"title": "Приглашение в команду", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли оператора."},
  "tenant_verified": {"title": "Организация верифицирована", "body": "Организация {{.organization_name}} верифицирована."},
  "operator_assigned": {"title": "Вам назначена техника", "body": "{{.operator_name}}, вы назначены оператором техники."},
  "operator_released": {"title": "Вы сняты с техники", "body": "{{.operator_name}}, вы сняты с управления техникой."},
  "wallet_operation_requested": {"title": "Запрошена операция по кошельку", "body": "Запрошена операция {{.operation_kind}} на сумму {{.amount}} {{.currency}}."},
  "wallet_operation_decided": {"title": "Операция по кошельку: {{.decision}}", "body": "Ваша операция по кошельку на сумму {{.amount}} {{.currency}}: {{.decision}}."},
  "tenant_rejected": {"title": "Организация отклонена", "body": "Ваша организация отклонена: {{.reason}}"},
  "invite_accepted": {"title": "Приглашение принято", "body": "{{.phone}} принял ваше приглашение в роли {{.role}}."},
  "invite_declined": {"title": "Приглашение отклонено", "body": "{{.phone}} отклонил ваше приглашение."},
  "admin_transferred_new": {"title": "Теперь вы администратор команды", "body": "Вы назначены администратором команды {{.team_name}}."},
  "admin_transferred_old": {"title": "Права администратора переданы", "body": "Вы передали права администратора команды {{.team_name}}."},
  "member_blocked": {"title": "Доступ временно ограничен", "body": "Ваш доступ к команде {{.team_name}} временно ограничен."},
  "member_unblocked": {"title": "Доступ восстановлен", "body": "Ваш доступ к команде {{.team_name}} восстановлен."},
  "member_removed": {"title": "Удалены из команды", "body": "Вы удалены из команды {{.team_name}}."},
  "team_member_removed_admin": {"title": "{{.removed_member_name}} покинул команду", "body": "{{.removed_member_name}} больше не состоит в команде {{.team_name}}."},
  "order_request_created": {"title": "Новый запрос на заказ", "body": "У вас новый запрос по «{{.listing_title}}»."},
  "order_request_accepted": {"title": "Запрос принят", "body": "Ваш запрос по «{{.listing_title}}» принят."},
  "order_terms_agreed": {"title": "Условия согласованы", "body": "Обе стороны согласовали условия по «{{.listing_title}}»."},
  "order_confirmed": {"title": "Заказ подтверждён", "body": "Заказ по «{{.listing_title}}» подтверждён."},
  "order_counter_offer_sent": {"title": "Получено встречное предложение", "body": "Отправлено встречное предложение по «{{.listing_title}}»."},
  "order_counter_offer_withdrawn": {"title": "Встречное предложение отозвано", "body": "Встречное предложение по «{{.listing_title}}» отозвано."},
  "order_cancelled": {"title": "Заказ отменён", "body": "Заказ по «{{.listing_title}}» отменён пользователем {{.cancelled_by}}."},
  "order_auto_cancelled": {"title": "Заказ отменён автоматически", "body": "Заказ по «{{.listing_title}}» был автоматически отменён."},
  "order_transferred": {"title": "Заказ передан", "body": "Заказ по «{{.listing_title}}» передан."},
  "order_receipt_confirmed": {"title": "Получение подтверждено", "body": "Получение подтверждено по «{{.listing_title}}»."},
  "order_auto_completed": {"title": "Заказ завершён автоматически", "body": "Заказ по «{{.listing_title}}» был автоматически завершён."},
  "order_review_window_ending": {"title": "Окно отзыва закрывается", "body": "Окно отзыва по «{{.listing_title}}» скоро закроется."}
}`)},
	}
	b, err := i18n.LoadBundle(fsys, language.English)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	return NewRenderer(b)
}

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

// ── New tests (Renderer API) ──────────────────────────────────────────────────

func TestRenderer_Render_Success(t *testing.T) {
	r := testRenderer(t)
	title, body, err := r.Render(
		notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED,
		map[string]string{"listing_title": "Excavator"},
		"en",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Listing approved" {
		t.Errorf("title = %q", title)
	}
	if body != "Your listing 'Excavator' is now live." {
		t.Errorf("body = %q", body)
	}
}

func TestRenderer_Render_MissingParam(t *testing.T) {
	r := testRenderer(t)
	_, _, err := r.Render(
		notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_APPROVED,
		map[string]string{}, // listing_title missing
		"en",
	)
	if err == nil {
		t.Fatal("expected ErrMissingParam, got nil")
	}
}

// ── Converted existing tests ──────────────────────────────────────────────────

func TestRenderEveryTypeEveryLocale(t *testing.T) {
	r := testRendererFull(t)
	locales := []string{"en", "ru"}
	for nt := range typeKey {
		for _, loc := range locales {
			t.Run(nt.String()+"_"+loc, func(t *testing.T) {
				title, body, err := r.Render(nt, validParamsFor(nt), loc)
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
	r := testRenderer(t)
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED
	params := map[string]string{"listing_title": "X"} // missing "reason"
	_, _, err := r.Render(nt, params, "en")
	assertReason(t, err, ReasonMissingParam)
	var appErr *commonerr.AppError
	if errors.As(err, &appErr) {
		if appErr.Params["param"] != "reason" {
			t.Errorf("params[param] = %v, want 'reason'", appErr.Params["param"])
		}
	}
}

func TestRenderUnknownType(t *testing.T) {
	r := testRenderer(t)
	_, _, err := r.Render(notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED,
		map[string]string{}, "en")
	assertReason(t, err, ReasonUnknownType)

	_, _, err = r.Render(notificationv1.NotificationType_NOTIFICATION_TYPE_SYSTEM,
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
	r := testRendererFull(t)
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
	for _, loc := range []string{"en", "ru"} {
		title, body, err := r.Render(nt, params, loc)
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
	r := testRendererFull(t)
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
			for _, loc := range []string{"en", "ru"} {
				title, body, err := r.Render(tc.nt, params, loc)
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
	r := testRendererFull(t)
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_TEAM_MEMBER_REMOVED_ADMIN
	params := map[string]string{"team_name": "Crew A", "removed_member_name": "Ivan"}
	title, body, err := r.Render(nt, params, "en")
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

// TestRenderOrderLifecycle_EveryLocale locks the 12 order-lifecycle catalog
// entries (order-service contracts, gen-go-lib NotificationType 27-38): each
// must render non-empty title/body in every locale with exactly its required
// params. Params are listed explicitly (not read back from requiredParams) so
// this test is red before catalog.go registers the types, and stays a real
// lock on the table afterward.
func TestRenderOrderLifecycle_EveryLocale(t *testing.T) {
	r := testRendererFull(t)
	cases := []struct {
		nt     notificationv1.NotificationType
		params map[string]string
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_CREATED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_ACCEPTED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TERMS_AGREED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CONFIRMED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_SENT, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_WITHDRAWN, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED, map[string]string{"listing_title": "Excavator", "cancelled_by": "customer"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_CANCELLED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TRANSFERRED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_RECEIPT_CONFIRMED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_COMPLETED, map[string]string{"listing_title": "Excavator"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_WINDOW_ENDING, map[string]string{"listing_title": "Excavator"}},
	}
	for _, tc := range cases {
		for _, loc := range []string{"en", "ru"} {
			t.Run(tc.nt.String()+"_"+loc, func(t *testing.T) {
				title, body, err := r.Render(tc.nt, tc.params, loc)
				if err != nil {
					t.Fatalf("Render returned err: %v", err)
				}
				if title == "" {
					t.Errorf("empty title for %s/%s", tc.nt, loc)
				}
				if body == "" {
					t.Errorf("empty body for %s/%s", tc.nt, loc)
				}
			})
		}
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
	r := testRenderer(t)
	_, _, rerr := r.Render(notificationv1.NotificationType_NOTIFICATION_TYPE_PLATFORM_MESSAGE, params, "en")
	assertReason(t, rerr, ReasonUnknownType)
}
