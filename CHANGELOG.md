# Changelog

## [Unreleased]

### Added
- `pkg/notifyrender` — typed template rendering for NotificationEnvelope payloads. Templates (TOML, en/ru/kk) embedded; `Render(type, params, locale)` + `ExtractParams(env)` + `RequiredParams(t)` public API. Both notification-service and inbox-service depend on it.
- `pkg/notifyoutbox` — typed producer wrapper for `TOPIC_NOTIFICATION_EVENTS`. `PublishDirective(ctx, pub, tx, env)` validates 10 rules before writing the outbox row. After Phase 4, direct `outbox.Publisher.PublishProto` for that topic is forbidden.
- Tolgee CI workflows (`.tolgeerc.json`, `i18n-{push,pull,consistency}.yml`, `scripts/check-i18n-consistency.sh`) — moves the in-flight Tolgee setup from notification-service to go-common, since templates now live here. Project ID placeholder; inert until ops provisions the Tolgee project.

### Changed
- `notifyoutbox.validate()` relaxed the recipient_user_id rule from "required unless channels is exactly `[SMS]`" to "required when channels contains IN_APP or PUSH". This unblocks anonymous flows (auth-service login OTPs by SMS or email, team-service phone-keyed invites) without changing the safety contract for in-app/push.
- `notifyrender.ExtractParams` gained cases for the three new auth-service payloads (`SendLoginOtpSms`, `SendLoginOtpEmail`, `SendNewDeviceLogin`). They are NOT in the in-app catalog — they go through notification-service's legacy email/SMS template path.
