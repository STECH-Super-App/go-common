# Changelog

## [Unreleased]

### Added
- `pkg/notifyrender` — typed template rendering for NotificationEnvelope payloads. Templates (TOML, en/ru/kk) embedded; `Render(type, params, locale)` + `ExtractParams(env)` + `RequiredParams(t)` public API. Both notification-service and inbox-service depend on it.
- `pkg/notifyoutbox` — typed producer wrapper for `TOPIC_NOTIFICATION_EVENTS`. `PublishDirective(ctx, pub, tx, env)` validates 10 rules before writing the outbox row. After Phase 4, direct `outbox.Publisher.PublishProto` for that topic is forbidden.
- Tolgee CI workflows (`.tolgeerc.json`, `i18n-{push,pull,consistency}.yml`, `scripts/check-i18n-consistency.sh`) — moves the in-flight Tolgee setup from notification-service to go-common, since templates now live here. Project ID placeholder; inert until ops provisions the Tolgee project.
- Inbox-notif slices 2 & 4 — nine new in-app `NotificationType` catalog entries with TOML (en/ru/kk): `TENANT_REJECTED`, `INVITE_ACCEPTED`, `INVITE_DECLINED`, `ADMIN_TRANSFERRED_NEW`, `ADMIN_TRANSFERRED_OLD` (slice 2 — EMAIL flows now also render IN_APP), and `MEMBER_BLOCKED`, `MEMBER_UNBLOCKED`, `MEMBER_REMOVED`, `TEAM_MEMBER_REMOVED_ADMIN` (slice 4 — team membership lifecycle). EN is real source-of-truth prose; ru/kk seeded with the `TODO_*` placeholder convention pending Tolgee.
- Inbox-notif slice 5 (verbatim free-text) — `notifyrender.IsVerbatim(t)` + `notifyrender.RenderVerbatim(params)` + `ReasonEmptyVerbatim`/`ErrEmptyVerbatimText`. `NOTIFICATION_TYPE_PLATFORM_MESSAGE` carries admin-authored literal title/body and is deliberately NOT in the catalog; consumers branch on `IsVerbatim` before `Render` and copy the text straight into the inbox row.

### Changed
- `notifyoutbox.validate()` relaxed the recipient_user_id rule from "required unless channels is exactly `[SMS]`" to "required when channels contains IN_APP or PUSH". This unblocks anonymous flows (auth-service login OTPs by SMS or email, team-service phone-keyed invites) without changing the safety contract for in-app/push.
- `notifyrender.ExtractParams` gained cases for the three new auth-service payloads (`SendLoginOtpSms`, `SendLoginOtpEmail`, `SendNewDeviceLogin`). They are NOT in the in-app catalog — they go through notification-service's legacy email/SMS template path.
- `notifyrender.ExtractParams` `SendAdminTransferredOld`/`SendAdminTransferredNew` cases now pull the new `team_name` field (was an empty map) so the in-app body names the team instead of rendering static text. Slice 4 adds the four member-lifecycle payload cases plus the slice-5 `SendPlatformMessage` verbatim case.
- `notifyoutbox.validateParams` gained a verbatim escape hatch: `PLATFORM_MESSAGE` skips the catalog/required-param contract and is validated only for non-empty title-or-body (`NOTIFYOUTBOX_EMPTY_VERBATIM`).
