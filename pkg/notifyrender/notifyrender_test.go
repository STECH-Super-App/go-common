package notifyrender

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

// newTestRenderer builds a Renderer over an OverlayBundle seeded with BaselineEN
// and the given overlay files. The files are written to a real temp dir (the
// overlay engine binds a directory path, not an fs.FS). The allow-list is the
// real per-key contract so overlay values are validated exactly as they are in
// production wiring.
func newTestRenderer(t *testing.T, files map[string]string) *Renderer {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	b := i18n.NewOverlayBundle(BaselineEN, dir, i18n.WithAllowedParams(AllowedParamsByKey))
	b.Load(context.Background())
	return NewRenderer(b)
}

// testRenderer builds a Renderer backed by a minimal in-memory bundle that
// only contains the listing_approved template. Use for error-path tests and
// tests that only need one type resolved.
func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	return newTestRenderer(t, map[string]string{
		"en.json": `{
			"listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."}
		}`,
	})
}

// testRendererFull builds a Renderer with the complete catalog in both en and
// ru locales. Use for tests that iterate typeKey or render multiple types.
func testRendererFull(t *testing.T) *Renderer {
	t.Helper()
	return newTestRenderer(t, map[string]string{
		"en.json": `{
  "chat_message": {"title": "New message from {{.sender_name}}", "body": "{{.preview}}"},
  "listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."},
  "listing_rejected": {"title": "Listing rejected", "body": "Your listing '{{.listing_title}}' was rejected: {{.reason}}"},
  "listing_unpublished": {"title": "Listing unpublished", "body": "Your listing '{{.listing_title}}' was unpublished: {{.reason}}"},
  "favorite_price_changed": {"title": "Price changed", "body": "The price of '{{.listing_title}}' changed from {{.old_price}} to {{.new_price}} {{.currency}}."},
  "favorite_listing_removed": {"title": "Favorite listing removed", "body": "'{{.listing_title}}' is no longer available."},
  "team_invite_tenant_manager": {"title": "Invitation to join {{.team_name}}", "body": "{{.inviter_name}} invited you to join {{.team_name}} as a manager."},
  "team_invite_tenant_operator": {"title": "Invitation to join {{.team_name}}", "body": "{{.inviter_name}} invited you to join {{.team_name}} as an operator."},
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
  "team_member_removed_admin": {"title": "{{.removed_member_name}} was removed from the team", "body": "{{.removed_member_name}} was removed from the team {{.team_name}}."},
  "team_member_left": {"title": "{{.member_name}} left the team", "body": "{{.member_name}} is no longer a member of the team {{.team_name}}."},
  "member_role_changed_manager": {"title": "Your role has changed", "body": "You are now a manager of the team {{.team_name}}."},
  "member_role_changed_operator": {"title": "Your role has changed", "body": "You are now an operator of the team {{.team_name}}."},
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
  "order_review_window_ending": {"title": "Review window ending", "body": "The review window for '{{.listing_title}}' is ending soon."},
  "admin_transfer_initiated": {"title": "Admin role transfer offer", "body": "{{.from_user_name}} wants to transfer the admin role of {{.organization_name}} to you. Respond before the offer expires."},
  "admin_transfer_accepted": {"title": "Admin transfer accepted", "body": "{{.to_user_name}} accepted the admin role of {{.organization_name}}."},
  "admin_transfer_rejected": {"title": "Admin transfer declined", "body": "{{.to_user_name}} declined the admin transfer for {{.organization_name}}."},
  "admin_transfer_cancelled": {"title": "Admin transfer cancelled", "body": "{{.from_user_name}} cancelled the admin transfer for {{.organization_name}}."},
  "admin_transfer_expired": {"title": "Admin transfer expired", "body": "The admin transfer for {{.organization_name}} with {{.counterparty_name}} expired without action."}
}`,
		"ru.json": `{
  "chat_message": {"title": "Новое сообщение от {{.sender_name}}", "body": "{{.preview}}"},
  "listing_approved": {"title": "Объявление одобрено", "body": "Ваше объявление «{{.listing_title}}» опубликовано."},
  "listing_rejected": {"title": "Объявление отклонено", "body": "Ваше объявление «{{.listing_title}}» отклонено: {{.reason}}"},
  "listing_unpublished": {"title": "Объявление снято с публикации", "body": "Ваше объявление «{{.listing_title}}» снято с публикации: {{.reason}}"},
  "favorite_price_changed": {"title": "Цена изменилась", "body": "Цена «{{.listing_title}}» изменилась с {{.old_price}} на {{.new_price}} {{.currency}}."},
  "favorite_listing_removed": {"title": "Избранное объявление удалено", "body": "«{{.listing_title}}» больше не доступно."},
  "team_invite_tenant_manager": {"title": "Приглашение в {{.team_name}}", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли менеджера."},
  "team_invite_tenant_operator": {"title": "Приглашение в {{.team_name}}", "body": "{{.inviter_name}} приглашает вас в {{.team_name}} в роли оператора."},
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
  "team_member_removed_admin": {"title": "{{.removed_member_name}} исключён из команды", "body": "{{.removed_member_name}} исключён из команды {{.team_name}}."},
  "team_member_left": {"title": "{{.member_name}} покинул команду", "body": "{{.member_name}} больше не состоит в команде {{.team_name}}."},
  "member_role_changed_manager": {"title": "Ваша роль изменена", "body": "Теперь вы менеджер команды {{.team_name}}."},
  "member_role_changed_operator": {"title": "Ваша роль изменена", "body": "Теперь вы оператор команды {{.team_name}}."},
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
  "order_review_window_ending": {"title": "Окно отзыва закрывается", "body": "Окно отзыва по «{{.listing_title}}» скоро закроется."},
  "admin_transfer_initiated": {"title": "Предложение передать права администратора", "body": "{{.from_user_name}} хочет передать вам роль администратора организации {{.organization_name}}. Ответьте до истечения срока действия запроса."},
  "admin_transfer_accepted": {"title": "Передача администратора принята", "body": "{{.to_user_name}} принял роль администратора организации {{.organization_name}}."},
  "admin_transfer_rejected": {"title": "Передача администратора отклонена", "body": "{{.to_user_name}} отклонил передачу прав администратора для {{.organization_name}}."},
  "admin_transfer_cancelled": {"title": "Передача администратора отменена", "body": "{{.from_user_name}} отменил передачу прав администратора для {{.organization_name}}."},
  "admin_transfer_expired": {"title": "Передача администратора истекла", "body": "Передача прав администратора для {{.organization_name}} с {{.counterparty_name}} истекла без действий."}
}`,
	})
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
		{
			name: "member_role_changed_manager",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_MANAGER,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendMemberRoleChanged{
					SendMemberRoleChanged: &notificationv1.SendMemberRoleChanged{
						TeamId: "tm-1", TeamName: "Crew A", NewRole: "MANAGER",
					},
				},
			},
			// new_role is intentionally not extracted: the role word is
			// localized via the per-role NotificationType, not a placeholder.
			want: map[string]string{"team_name": "Crew A"},
		},
		{
			name: "member_role_changed_operator",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_OPERATOR,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendMemberRoleChanged{
					SendMemberRoleChanged: &notificationv1.SendMemberRoleChanged{
						TeamId: "tm-1", TeamName: "Crew A", NewRole: "OPERATOR",
					},
				},
			},
			want: map[string]string{"team_name": "Crew A"},
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

// TestExtractAndRender_AdminTransferLifecycle locks the five tenant
// admin-transfer directives: each payload extracts exactly its params and
// renders non-empty title/body in en and ru. reason is extracted for the
// rejected payload but is intentionally absent from the template and from
// requiredParams (it is optional at the HTTP boundary and cannot be
// localized), so it never reaches a placeholder.
func TestExtractAndRender_AdminTransferLifecycle(t *testing.T) {
	r := testRendererFull(t)
	cases := []struct {
		name string
		nt   notificationv1.NotificationType
		env  *notificationv1.NotificationEnvelope
		want map[string]string
	}{
		{
			name: "admin_transfer_initiated",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_INITIATED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferInitiated{
					SendAdminTransferInitiated: &notificationv1.SendAdminTransferInitiated{
						OrganizationName: "Acme LLC", FromUserName: "Ivan",
					},
				},
			},
			want: map[string]string{"organization_name": "Acme LLC", "from_user_name": "Ivan"},
		},
		{
			name: "admin_transfer_accepted",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_ACCEPTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferAccepted{
					SendAdminTransferAccepted: &notificationv1.SendAdminTransferAccepted{
						OrganizationName: "Acme LLC", ToUserName: "Petr",
					},
				},
			},
			want: map[string]string{"organization_name": "Acme LLC", "to_user_name": "Petr"},
		},
		{
			name: "admin_transfer_rejected",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_REJECTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferRejected{
					SendAdminTransferRejected: &notificationv1.SendAdminTransferRejected{
						OrganizationName: "Acme LLC", ToUserName: "Petr", Reason: "changed mind",
					},
				},
			},
			want: map[string]string{"organization_name": "Acme LLC", "to_user_name": "Petr", "reason": "changed mind"},
		},
		{
			name: "admin_transfer_cancelled",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_CANCELLED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferCancelled{
					SendAdminTransferCancelled: &notificationv1.SendAdminTransferCancelled{
						OrganizationName: "Acme LLC", FromUserName: "Ivan",
					},
				},
			},
			want: map[string]string{"organization_name": "Acme LLC", "from_user_name": "Ivan"},
		},
		{
			name: "admin_transfer_expired",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ADMIN_TRANSFER_EXPIRED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendAdminTransferExpired{
					SendAdminTransferExpired: &notificationv1.SendAdminTransferExpired{
						OrganizationName: "Acme LLC", CounterpartyName: "Petr",
					},
				},
			},
			want: map[string]string{"organization_name": "Acme LLC", "counterparty_name": "Petr"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := ExtractParams(tc.env)
			if err != nil {
				t.Fatalf("ExtractParams err: %v", err)
			}
			if len(params) != len(tc.want) {
				t.Fatalf("param count = %d, want %d (%v)", len(params), len(tc.want), params)
			}
			for k, v := range tc.want {
				if params[k] != v {
					t.Errorf("param[%q] = %q, want %q", k, params[k], v)
				}
			}
			for _, loc := range []string{"en", "ru"} {
				title, body, err := r.Render(tc.nt, params, loc)
				if err != nil {
					t.Fatalf("Render(%s): %v", loc, err)
				}
				if title == "" || body == "" {
					t.Errorf("Render(%s) returned empty title/body", loc)
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

// TestExtractParams_OrderLifecycle locks the ExtractParams mapping for all 12
// SendOrder* payloads (order-service contracts, gen-go-lib NotificationType
// 27-38 / oneof fields 51-62): each must extract exactly its catalog params
// (listing_title for all, plus cancelled_by for SendOrderCancelled) and not
// fall through to the default ErrEmptyPayload branch.
func TestExtractParams_OrderLifecycle(t *testing.T) {
	r := testRendererFull(t)
	cases := []struct {
		name string
		nt   notificationv1.NotificationType
		env  *notificationv1.NotificationEnvelope
		want map[string]string
	}{
		{
			name: "order_request_created",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_CREATED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderRequestCreated{
					SendOrderRequestCreated: &notificationv1.SendOrderRequestCreated{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_request_accepted",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_ACCEPTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderRequestAccepted{
					SendOrderRequestAccepted: &notificationv1.SendOrderRequestAccepted{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_terms_agreed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TERMS_AGREED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderTermsAgreed{
					SendOrderTermsAgreed: &notificationv1.SendOrderTermsAgreed{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_confirmed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CONFIRMED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderConfirmed{
					SendOrderConfirmed: &notificationv1.SendOrderConfirmed{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_counter_offer_sent",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_SENT,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderCounterOfferSent{
					SendOrderCounterOfferSent: &notificationv1.SendOrderCounterOfferSent{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_counter_offer_withdrawn",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_COUNTER_OFFER_WITHDRAWN,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderCounterOfferWithdrawn{
					SendOrderCounterOfferWithdrawn: &notificationv1.SendOrderCounterOfferWithdrawn{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_cancelled",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderCancelled{
					SendOrderCancelled: &notificationv1.SendOrderCancelled{
						OrderId: "order-1", ListingTitle: "Excavator", CancelledBy: "customer",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator", "cancelled_by": "customer"},
		},
		{
			name: "order_auto_cancelled",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_CANCELLED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderAutoCancelled{
					SendOrderAutoCancelled: &notificationv1.SendOrderAutoCancelled{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_transferred",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_TRANSFERRED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderTransferred{
					SendOrderTransferred: &notificationv1.SendOrderTransferred{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_receipt_confirmed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_RECEIPT_CONFIRMED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderReceiptConfirmed{
					SendOrderReceiptConfirmed: &notificationv1.SendOrderReceiptConfirmed{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_auto_completed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_COMPLETED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderAutoCompleted{
					SendOrderAutoCompleted: &notificationv1.SendOrderAutoCompleted{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
		},
		{
			name: "order_review_window_ending",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_WINDOW_ENDING,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendOrderReviewWindowEnding{
					SendOrderReviewWindowEnding: &notificationv1.SendOrderReviewWindowEnding{
						OrderId: "order-1", ListingTitle: "Excavator",
					},
				},
			},
			want: map[string]string{"listing_title": "Excavator"},
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

// TestExtractParams_NilPayload locks the shared error path: an envelope with
// no payload set (covers the SendOrder* additions same as every other
// payload) must return ErrEmptyPayload, never a panic or a zero-value map.
func TestExtractParams_NilPayload(t *testing.T) {
	_, err := ExtractParams(&notificationv1.NotificationEnvelope{})
	assertReason(t, err, ReasonEmptyPayload)
}

// TestExtractParams_SendTenantDocumentsRequested locks the ExtractParams
// mapping for the tenant-service EMAIL/SYSTEM directive: it must extract
// tenant_id, comment, and the repeated reasons joined with ", " into a single
// string value — and return a nil error so the caller does not dead-letter it.
func TestExtractParams_SendTenantDocumentsRequested(t *testing.T) {
	env := &notificationv1.NotificationEnvelope{
		Payload: &notificationv1.NotificationEnvelope_SendTenantDocumentsRequested{
			SendTenantDocumentsRequested: &notificationv1.SendTenantDocumentsRequested{
				TenantId: "tenant-42",
				Reasons:  []string{"blurry scan", "expired license"},
				Comment:  "please resubmit clearer photos",
			},
		},
	}

	params, err := ExtractParams(env)
	if err != nil {
		t.Fatalf("ExtractParams returned err: %v", err)
	}

	want := map[string]string{
		"tenant_id": "tenant-42",
		"comment":   "please resubmit clearer photos",
		"reasons":   "blurry scan, expired license",
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

// TestExtractParams_UnmappedPayload locks the fixed default arm: a payload that
// IS set but has no case in the switch must return ErrUnknownType
// (ReasonUnknownType), NOT ErrEmptyPayload. Before the fix, a set-but-unmapped
// directive masqueraded as empty and got dead-lettered as unrecoverable.
// SendContactPhoneOtpSms is a deliberately-unmapped payload used only to
// exercise this arm; if a case is ever added for it, swap in another unmapped
// variant.
func TestExtractParams_UnmappedPayload(t *testing.T) {
	env := &notificationv1.NotificationEnvelope{
		Metadata: &notificationv1.EnvelopeMetadata{
			Type: notificationv1.NotificationType_NOTIFICATION_TYPE_SYSTEM,
		},
		Payload: &notificationv1.NotificationEnvelope_SendContactPhoneOtpSms{
			SendContactPhoneOtpSms: &notificationv1.SendContactPhoneOtpSms{
				Phone: "+77001234567",
				Code:  "1234",
			},
		},
	}

	_, err := ExtractParams(env)
	assertReason(t, err, ReasonUnknownType)
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

// TestRenderDeliveryLifecycle_Baseline renders every one of the 18 delivery
// catalog types (order-service delivery vertical, gen-go-lib NotificationType
// 47-64) straight from the shipping BaselineEN (empty overlay dir → baseline is
// the only layer). It asserts a non-empty title/body with no leftover "{{"/"}}"
// template markers, and — for the three required-param types — that the param
// value actually reaches the output (a real interpolation lock, not a
// tautology).
//
// request_no is deliberately absent from every params map here: it is an
// OPTIONAL param, so a caller that never supplies the key must still render.
// This exercises Render's absent-optional fill — without it the {{if}} guard
// would trip missingkey=error and every delivery notification would 500. The
// no-dangling-"#" assertion is the visible half of the same contract.
func TestRenderDeliveryLifecycle_Baseline(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	cases := []struct {
		nt     notificationv1.NotificationType
		params map[string]string
		wantIn []string // substrings that MUST appear in title+body
	}{
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_ACCEPTED, map[string]string{"final_price": "1 500 ₽", "currency": "RUB"}, []string{"1 500 ₽", "RUB"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_REJECTED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_SENT, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_ACCEPTED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_DECLINED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_WITHDRAWN, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED, map[string]string{"cancelled_by": "The customer"}, []string{"The customer"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY, map[string]string{"route": "Almaty to Astana"}, []string{"Almaty to Astana"}},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_IN_TRANSIT, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AWAITING_RECEIPT, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_RECEIPT_REMINDER, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AUTO_CONFIRMED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_COMPLETED, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_INVITE, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_WINDOW_ENDING, map[string]string{}, nil},
		{notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED, map[string]string{}, nil},
	}
	if len(cases) != 18 {
		t.Fatalf("expected 18 delivery cases, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.nt.String(), func(t *testing.T) {
			title, body, err := r.Render(tc.nt, tc.params, "en")
			if err != nil {
				t.Fatalf("Render returned err: %v", err)
			}
			if title == "" || body == "" {
				t.Fatalf("empty title/body (title=%q body=%q)", title, body)
			}
			for _, s := range []string{title, body} {
				if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
					t.Errorf("leftover template marker in %q", s)
				}
				// An unguarded {{.request_no}} would leave the literal "#" behind
				// once the empty value interpolated.
				if strings.Contains(s, "#") {
					t.Errorf("dangling number marker in %q (request_no was not supplied)", s)
				}
			}
			joined := title + " " + body
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Errorf("rendered output %q does not contain param value %q", joined, want)
				}
			}
		})
	}
}

// TestExtractAndRender_DeliveryLifecycle drives the FULL live inbox path
// (envelope → ExtractParams → Render) for every one of the 18 delivery types
// (order-service delivery vertical, gen-go-lib NotificationType 47-64). This is
// the test that would have caught a missing ExtractParams case: without a switch
// arm the typed payload falls through to the default and returns ErrUnknownType,
// so inbox-service would dead-letter the directive. Each case asserts:
//   - ExtractParams returns exactly the type's declared params (empty only for
//     the payloads that carry no fields at all; the 9 that carry request_no
//     surface it),
//   - Render lands a non-empty title/body with no leftover "{{"/"}}" markers,
//   - the param-bearing types interpolate their value into the output (a real
//     lock, not a tautology) — the accepted case asserts the pre-formatted price,
//     and every request_no case asserts the "#<number>" reaches the text.
func TestExtractAndRender_DeliveryLifecycle(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	cases := []struct {
		name   string
		nt     notificationv1.NotificationType
		env    *notificationv1.NotificationEnvelope
		want   map[string]string
		wantIn []string // substrings that MUST appear in title+body
	}{
		{
			name: "request_created",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestCreated{
					SendDeliveryRequestCreated: &notificationv1.SendDeliveryRequestCreated{RequestNo: "R-001"},
				},
			},
			want:   map[string]string{"request_no": "R-001"},
			wantIn: []string{"#R-001"},
		},
		{
			name: "request_accepted",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_ACCEPTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestAccepted{
					SendDeliveryRequestAccepted: &notificationv1.SendDeliveryRequestAccepted{
						FinalPrice: "1 500 ₽", Currency: "RUB",
					},
				},
			},
			want:   map[string]string{"final_price": "1 500 ₽", "currency": "RUB"},
			wantIn: []string{"1 500 ₽", "RUB"},
		},
		{
			name: "request_rejected",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_REJECTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestRejected{
					SendDeliveryRequestRejected: &notificationv1.SendDeliveryRequestRejected{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "counter_offer_sent",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_SENT,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryCounterOfferSent{
					SendDeliveryCounterOfferSent: &notificationv1.SendDeliveryCounterOfferSent{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "counter_offer_accepted",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_ACCEPTED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryCounterOfferAccepted{
					SendDeliveryCounterOfferAccepted: &notificationv1.SendDeliveryCounterOfferAccepted{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "counter_offer_declined",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_DECLINED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryCounterOfferDeclined{
					SendDeliveryCounterOfferDeclined: &notificationv1.SendDeliveryCounterOfferDeclined{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "counter_offer_withdrawn",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_COUNTER_OFFER_WITHDRAWN,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryCounterOfferWithdrawn{
					SendDeliveryCounterOfferWithdrawn: &notificationv1.SendDeliveryCounterOfferWithdrawn{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "request_cancelled",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED,
			// CancelReason is set to prove it does NOT leak into params (it is
			// neither templated nor declared); RequestNo does reach the text.
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestCancelled{
					SendDeliveryRequestCancelled: &notificationv1.SendDeliveryRequestCancelled{
						CancelledBy: "The customer", CancelReason: "changed plans", RequestNo: "R-002",
					},
				},
			},
			want:   map[string]string{"cancelled_by": "The customer", "request_no": "R-002"},
			wantIn: []string{"The customer", "#R-002"},
		},
		{
			name: "loading_today",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryLoadingToday{
					SendDeliveryLoadingToday: &notificationv1.SendDeliveryLoadingToday{
						Route: "Almaty to Astana", RequestNo: "R-003",
					},
				},
			},
			want:   map[string]string{"route": "Almaty to Astana", "request_no": "R-003"},
			wantIn: []string{"Almaty to Astana", "#R-003"},
		},
		{
			name: "request_expired",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestExpired{
					SendDeliveryRequestExpired: &notificationv1.SendDeliveryRequestExpired{RequestNo: "R-004"},
				},
			},
			want:   map[string]string{"request_no": "R-004"},
			wantIn: []string{"#R-004"},
		},
		{
			name: "in_transit",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_IN_TRANSIT,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryInTransit{
					SendDeliveryInTransit: &notificationv1.SendDeliveryInTransit{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "awaiting_receipt",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AWAITING_RECEIPT,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryAwaitingReceipt{
					SendDeliveryAwaitingReceipt: &notificationv1.SendDeliveryAwaitingReceipt{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "receipt_reminder",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_RECEIPT_REMINDER,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryReceiptReminder{
					SendDeliveryReceiptReminder: &notificationv1.SendDeliveryReceiptReminder{RequestNo: "R-005"},
				},
			},
			want:   map[string]string{"request_no": "R-005"},
			wantIn: []string{"#R-005"},
		},
		{
			name: "auto_confirmed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AUTO_CONFIRMED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryAutoConfirmed{
					SendDeliveryAutoConfirmed: &notificationv1.SendDeliveryAutoConfirmed{RequestNo: "R-006"},
				},
			},
			want:   map[string]string{"request_no": "R-006"},
			wantIn: []string{"#R-006"},
		},
		{
			name: "request_completed",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_COMPLETED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryRequestCompleted{
					SendDeliveryRequestCompleted: &notificationv1.SendDeliveryRequestCompleted{},
				},
			},
			want: map[string]string{},
		},
		{
			name: "review_invite",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_INVITE,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryReviewInvite{
					SendDeliveryReviewInvite: &notificationv1.SendDeliveryReviewInvite{RequestNo: "R-007"},
				},
			},
			want:   map[string]string{"request_no": "R-007"},
			wantIn: []string{"#R-007"},
		},
		{
			name: "review_window_ending",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_WINDOW_ENDING,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryReviewWindowEnding{
					SendDeliveryReviewWindowEnding: &notificationv1.SendDeliveryReviewWindowEnding{RequestNo: "R-008"},
				},
			},
			want:   map[string]string{"request_no": "R-008"},
			wantIn: []string{"#R-008"},
		},
		{
			name: "cascade_cancelled",
			nt:   notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED,
			env: &notificationv1.NotificationEnvelope{
				Payload: &notificationv1.NotificationEnvelope_SendDeliveryCascadeCancelled{
					SendDeliveryCascadeCancelled: &notificationv1.SendDeliveryCascadeCancelled{RequestNo: "R-009"},
				},
			},
			want:   map[string]string{"request_no": "R-009"},
			wantIn: []string{"#R-009"},
		},
	}
	if len(cases) != 18 {
		t.Fatalf("expected 18 delivery cases, got %d", len(cases))
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
			title, body, err := r.Render(tc.nt, params, "en")
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if title == "" || body == "" {
				t.Fatalf("empty title/body (title=%q body=%q)", title, body)
			}
			for _, s := range []string{title, body} {
				if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
					t.Errorf("leftover template marker in %q", s)
				}
			}
			joined := title + " " + body
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Errorf("rendered output %q does not contain %q", joined, want)
				}
			}
		})
	}
}

// deliveryRequestNoCase pins the EXACT rendered body for one request_no-bearing
// delivery type in both states. Exact strings (not substring probes) are the
// point: they are the reviewable record of the shipped copy, and `without` is
// byte-identical to what the type rendered before numbering shipped.
type deliveryRequestNoCase struct {
	name        string
	nt          notificationv1.NotificationType
	otherParams map[string]string // the type's REQUIRED params, if any
	withNumber  string
	without     string
}

// deliveryRequestNoCases covers all 9 delivery types whose payload carries
// request_no (Д-13, «Заявка #N»). Placement follows the vault push-text matrix
// («Уведомления перевозки» → "Тексты push-уведомлений"); the two types the
// matrix gives no push line for (review_window_ending, cascade_cancelled) reuse
// the «по заявке #N» / «Заявка #N» shape of their siblings.
var deliveryRequestNoCases = []deliveryRequestNoCase{
	{
		name:       "request_created",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED,
		withNumber: "You have a new delivery request #12345. Review it.",
		without:    "You have a new delivery request. Review it.",
	},
	{
		name:        "request_cancelled",
		nt:          notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CANCELLED,
		otherParams: map[string]string{"cancelled_by": "The customer"},
		withNumber:  "The customer cancelled request #12345.",
		without:     "The customer cancelled the request.",
	},
	{
		name:        "loading_today",
		nt:          notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_LOADING_TODAY,
		otherParams: map[string]string{"route": "Almaty to Astana"},
		withNumber:  "Loading is today for your request #12345 (Almaty to Astana).",
		without:     "Loading is today for your request (Almaty to Astana).",
	},
	{
		name:       "request_expired",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_EXPIRED,
		withNumber: "Request #12345 expired — the loading date has passed. You can repeat the request.",
		without:    "The request expired — the loading date has passed. You can repeat the request.",
	},
	{
		name:       "receipt_reminder",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_RECEIPT_REMINDER,
		withNumber: "Confirm receipt of the equipment for request #12345 — without a response the request auto-confirms in 24 hours.",
		without:    "Confirm receipt of the equipment — without a response the request auto-confirms in 24 hours.",
	},
	{
		name:       "auto_confirmed",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_AUTO_CONFIRMED,
		withNumber: "Request #12345 was auto-confirmed. You can leave a review within 14 days.",
		without:    "The request was auto-confirmed. You can leave a review within 14 days.",
	},
	{
		name:       "review_invite",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_INVITE,
		withNumber: "Rate the carrier for request #12345 — you have 14 days.",
		without:    "Rate the carrier — you have 14 days.",
	},
	{
		name:       "review_window_ending",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REVIEW_WINDOW_ENDING,
		withNumber: "Your review window for request #12345 is closing — 2 days left.",
		without:    "Your review window is closing — 2 days left.",
	},
	{
		name:       "cascade_cancelled",
		nt:         notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_CASCADE_CANCELLED,
		withNumber: "Delivery request #12345 was cancelled because the linked rental was cancelled.",
		without:    "The delivery was cancelled because the linked rental was cancelled.",
	},
}

// paramsWith returns the case's required params plus the given request_no
// binding. A nil extra leaves request_no out of the map entirely.
func (c deliveryRequestNoCase) paramsWith(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range c.otherParams {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TestRenderDelivery_RequestNumber is the copy lock for Д-13 request numbering.
// For each of the 9 request_no-bearing delivery types it asserts the exact body
// in three states:
//
//	populated — the number renders in the vault-specified position,
//	empty     — an old event (numbering not yet stamped) renders the plain
//	            sentence, no dangling "#",
//	absent    — a caller that never supplies the key renders identically to
//	            empty (Render's absent-optional fill), rather than 500ing on
//	            missingkey=error.
func TestRenderDelivery_RequestNumber(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	if len(deliveryRequestNoCases) != 9 {
		t.Fatalf("expected 9 request_no-bearing delivery cases, got %d", len(deliveryRequestNoCases))
	}
	for _, tc := range deliveryRequestNoCases {
		t.Run(tc.name, func(t *testing.T) {
			states := []struct {
				state string
				extra map[string]string
				want  string
			}{
				{"populated", map[string]string{"request_no": "12345"}, tc.withNumber},
				{"empty", map[string]string{"request_no": ""}, tc.without},
				{"absent", nil, tc.without},
			}
			for _, st := range states {
				t.Run(st.state, func(t *testing.T) {
					_, body, err := r.Render(tc.nt, tc.paramsWith(st.extra), "en")
					if err != nil {
						t.Fatalf("Render err: %v", err)
					}
					if body != st.want {
						t.Errorf("body =\n  %q\nwant\n  %q", body, st.want)
					}
					if st.state != "populated" && strings.Contains(body, "#") {
						t.Errorf("dangling number marker in %q", body)
					}
				})
			}
		})
	}
}

// TestDeliveryRequestNoIsOptionalNotRequired locks the producer-side contract:
// request_no must be declared OPTIONAL, never required. notifyoutbox rejects a
// directive whose REQUIRED param is empty (empty == missing there), so promoting
// request_no would make every legacy or not-yet-numbered delivery directive fail
// to publish — the opposite of graceful absence.
func TestDeliveryRequestNoIsOptionalNotRequired(t *testing.T) {
	for _, tc := range deliveryRequestNoCases {
		t.Run(tc.name, func(t *testing.T) {
			if !contains(OptionalParams(tc.nt), "request_no") {
				t.Errorf("%v: request_no missing from OptionalParams", tc.nt)
			}
			if contains(RequiredParams(tc.nt), "request_no") {
				t.Errorf("%v: request_no must NOT be a required param", tc.nt)
			}
			// The overlay allow-list must admit it, or an i18n-catalog translation
			// carrying the number would be silently rejected at load.
			allowed, ok := AllowedParamsByKey(typeKeyForTest()[tc.nt] + ".body")
			if !ok || !contains(allowed, "request_no") {
				t.Errorf("%v: AllowedParamsByKey does not admit request_no (allowed=%v)", tc.nt, allowed)
			}
		})
	}
}

// TestOptionalParamsScopedToDelivery asserts the optional tier did not leak
// beyond the 9 delivery types: no other catalog type declares an optional param,
// and no other baseline string mentions request_no. The rent vertical (order_*)
// and every platform type must be untouched by this change.
func TestOptionalParamsScopedToDelivery(t *testing.T) {
	declared := map[notificationv1.NotificationType]bool{}
	for _, tc := range deliveryRequestNoCases {
		declared[tc.nt] = true
	}
	for typ, params := range optionalParams {
		if !declared[typ] {
			t.Errorf("unexpected optional params %v on type %v", params, typ)
		}
	}
	if len(optionalParams) != len(declared) {
		t.Errorf("optionalParams has %d entries, want %d", len(optionalParams), len(declared))
	}

	for key, tmpl := range BaselineEN {
		if !strings.Contains(tmpl, "request_no") {
			continue
		}
		section := strings.TrimSuffix(strings.TrimSuffix(key, ".title"), ".body")
		if !declared[sectionToType[section]] {
			t.Errorf("baseline %q references request_no but its type does not declare it", key)
		}
	}
}

// TestRenderRentLifecycle_UnaffectedByRequestNumber renders the rent vertical
// (order-service's order_* types) and asserts the shipped bodies are exactly
// what they were before request numbering — no optional fill, no stray "#".
// The two verticals share the catalog, so a regression here would be invisible
// in the delivery tests.
func TestRenderRentLifecycle_UnaffectedByRequestNumber(t *testing.T) {
	r := newTestRenderer(t, map[string]string{}) // BaselineEN only, no overlay
	cases := []struct {
		nt     notificationv1.NotificationType
		params map[string]string
		want   string
	}{
		{
			notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REQUEST_CREATED,
			map[string]string{"listing_title": "Excavator XL"},
			"You have a new rental request for 'Excavator XL'.",
		},
		{
			notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED,
			map[string]string{"listing_title": "Excavator XL", "cancelled_by": "The owner"},
			"The order for 'Excavator XL' was cancelled by The owner.",
		},
		{
			notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_AUTO_COMPLETED,
			map[string]string{"listing_title": "Excavator XL"},
			"The order for 'Excavator XL' was completed automatically.",
		},
		{
			notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_REVIEW_WINDOW_ENDING,
			map[string]string{"listing_title": "Excavator XL"},
			"Your review window for 'Excavator XL' ends in 2 days.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.nt.String(), func(t *testing.T) {
			if len(OptionalParams(tc.nt)) != 0 {
				t.Fatalf("rent type %v unexpectedly declares optional params", tc.nt)
			}
			_, body, err := r.Render(tc.nt, tc.params, "en")
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if body != tc.want {
				t.Errorf("body = %q, want %q", body, tc.want)
			}
			if strings.Contains(body, "#") {
				t.Errorf("rent body %q unexpectedly contains a number marker", body)
			}
		})
	}
}

// TestOverlayMayTranslateRequestNumber proves the i18n-catalog path: a ru overlay
// value that guards request_no exactly like the baseline survives the
// WithAllowedParams policy and renders through. Without request_no in
// AllowedParamsByKey the overlay value would be dropped at load and the reader
// would silently fall back to English.
func TestOverlayMayTranslateRequestNumber(t *testing.T) {
	r := newTestRenderer(t, map[string]string{
		"ru.json": `{
  "delivery_request_created": {
    "title": "Новая заявка на перевозку",
    "body": "Новая заявка на перевозку{{if .request_no}} #{{.request_no}}{{end}}. Рассмотрите заявку."
  }
}`,
	})
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_DELIVERY_REQUEST_CREATED

	_, body, err := r.Render(nt, map[string]string{"request_no": "12345"}, "ru")
	if err != nil {
		t.Fatalf("Render ru err: %v", err)
	}
	if want := "Новая заявка на перевозку #12345. Рассмотрите заявку."; body != want {
		t.Errorf("ru body = %q, want %q", body, want)
	}

	_, body, err = r.Render(nt, map[string]string{"request_no": ""}, "ru")
	if err != nil {
		t.Fatalf("Render ru (empty number) err: %v", err)
	}
	if want := "Новая заявка на перевозку. Рассмотрите заявку."; body != want {
		t.Errorf("ru body (empty number) = %q, want %q", body, want)
	}
}
