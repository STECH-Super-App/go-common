# go-common

Shared library for STECH microservices.

## Packages

### `pkg/config`
Helper functions for loading configuration from environment variables.
- `GetEnv(key, default)`
- `GetEnvInt(key, default)`
- `GetEnvBool(key, default)`
- `GetEnvDuration(key, default)`

### `pkg/db`
Database connection factories.
- **Postgres**: `NewPostgres` creates a `*pgxpool.Pool` (using `jackc/pgx/v5`).
- **Redis**: `NewRedis` creates a `*redis.Client` (using `redis/go-redis/v9`).

### `pkg/input`
The fleet's one validation package for every value entering a service — REST body, path, query, gRPC field. Pure functions, no Echo/HTTP/logging, usable inside domain constructors. Spec: dev-setup `docs/superpowers/specs/2026-10-02-input-validation-standard-design.md` §4.

**Every validator returns the cleaned value; store, forward and publish only that value, never the raw input.** Caps are mandatory by signature (text max in **runes**, numbers min/max); a non-positive text cap or an inverted range panics at the call site.

```go
func Line(raw string, maxRunes int, opts ...TextOpt) (string, *Failure) // single-line
func Body(raw string, maxRunes int, opts ...TextOpt) (string, *Failure) // multi-line (TAB/LF/CR allowed)
func Required() TextOpt
func UUID(raw string) (string, *Failure)                                  // strict 8-4-4-4-12, any case in, lower case out
func Int32(v int64, lo, hi int32) (int32, *Failure)
func Int64(v int64, lo, hi int64) (int64, *Failure)
func ParseInt32(raw string, lo, hi int32) (int32, *Failure)              // decimal only: optional '-', digits
func ParseInt64(raw string, lo, hi int64) (int64, *Failure)
func ParsePage(rawNumber, rawSize string, defaultSize, maxSize int32) (Page, *Failure)
func MaxItems(n, maxItems int) *Failure
func Phone(raw string) (string, *Failure)                                 // '+' required, libphonenumber IsValidNumber, E.164 out
func Email(raw string, maxRunes int) (string, *Failure)                   // domain lower-cased, no DNS/IDN
func Collect(fields ...*errors.FieldError) []errors.FieldError            // drops nils; nil when nothing failed
func GRPC(fields ...errors.FieldError) error                              // InvalidArgument + errdetails.BadRequest; nil for no fields
func MessageFor(reason string) string                                     // English fallback for AppError.Message
```

`Line`/`Body` processing order: (1) invalid UTF-8 → `COMMON_TEXT_INVALID_CHARACTERS`; (2) trim leading/trailing whitespace, `Cc` and `Cf`; (3) character rule — any `Cc` (Body exempts TAB/LF/CR), and in `Line` also U+200E/F, U+202A–E, U+2066–9, U+2028/9 → `COMMON_TEXT_INVALID_CHARACTERS`. The rule is a copy of sale-service `app/Support/Validation/ControlCharacters.php`, pinned by a port of its unit vectors and an exhaustive all-code-points test. Interior `Cf` (ZWJ, ZWSP, BOM) is allowed — emoji sequences need ZWJ; (4) blank (only whitespace / `Cf` / variation selectors / default-ignorables) → `COMMON_TEXT_REQUIRED` with `Required()`, else `""`; (5) rune count > max → `COMMON_TEXT_TOO_LONG {max}`. No Unicode normalisation.

`*Failure` (`Reason`, `Params`) implements `error`, so a domain constructor can return it and a handler finds it with `errors.As`. The library is **status-free**: the service picks the HTTP status. Field names are attached by whoever knows them:

```go
name, nameF := input.Line(req.Name, 120, input.Required())
id, idF := input.UUID(c.Param("id"))
if details := input.Collect(nameF.At("name"), idF.At("id")); details != nil {
    return response.JSONError(c, commonErrors.New(http.StatusBadRequest).
        Reason(details[0].Reason).Message(details[0].Message).Details(details).Build())
}
```

`At` is nil-safe (a nil `*Failure` yields nil), which is what makes the `Collect` idiom work. A `ParsePage` failure names the input that failed through `AtPage(numberField, sizeField)`. `Phone` additionally refuses an interior control/format character and an extension (E.164 cannot carry one). Stricter domain rules (organisation's +7, VIN grammar) stay in the service, applied to the cleaned value.

Reasons (exported as `Reason*` constants): `COMMON_TEXT_REQUIRED`, `COMMON_TEXT_TOO_LONG {max}`, `COMMON_TEXT_INVALID_CHARACTERS`, `COMMON_UUID_INVALID`, `COMMON_NUMBER_INVALID`, `COMMON_NUMBER_OUT_OF_RANGE {min,max}`, `COMMON_TOO_MANY_ITEMS {max}`, `COMMON_PHONE_INVALID`, `COMMON_EMAIL_INVALID`, `COMMON_INPUT_REJECTED_BY_STORAGE` (the `pgerr` backstop), `COMMON_REQUEST_ENCODING_INVALID` (api-gateway).

### `pkg/db/pgerr`
The repository-level backstop for input that reached Postgres without a validator. Call it as the **first line of every `mapPgError`**:

```go
func mapPgError(err error) error {
    if f, ok := pgerr.InvalidInput(err); ok {
        return f // or wrap as a 4xx AppError; f.Error() names sqlstate/table/column for the log
    }
    // ... constraint-name mapping (Critical Rule 3) ...
}
```

`InvalidInput(err) (*input.Failure, bool)` recognises `*pgconn.PgError` SQLSTATE `22021`, `22P05`, `22001`, `22P02`, and pgx v5's client-side integer out-of-range encode error (no exported type; matched on its message, pinned by a test that produces the real error through `pgx.ExtendedQueryBuilder`). A hit returns a `COMMON_INPUT_REJECTED_BY_STORAGE` Failure whose cause carries the SQLSTATE and Postgres' table/column (logged, never sent to the client), and increments `stech_input_backstop_total{sqlstate}` (`22021|22P05|22001|22P02|encode`, all five pre-created at 0) on `metrics.Registry`. Any increase means a missing `pkg/input` call — the alert is `increase(stech_input_backstop_total[15m]) > 0`.

### `pkg/errors`
Typed application-error envelope returned to HTTP clients.

`AppError` carries a stable machine-readable `Reason`, optional `Params` for variable data the frontend interpolates into a localized string, and optional `Details []FieldError` for field-level validation. `Message` is an English-only log/dev fallback — clients localize from `Reason` + `Params`.

```go
import (
    "net/http"

    commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

return commonErrors.New(http.StatusGone).
    Reason("TENANT_ADMIN_TRANSFER_EXPIRED").
    Message("admin transfer expired").
    Params(map[string]any{
        "expiry_time":  t.ExpiryTime.Format(time.RFC3339),
        "transfer_id":  t.ID,
    }).
    Cause(err).
    Build()
```

For multi-field validation use `Details`:

```go
return commonErrors.New(http.StatusBadRequest).
    Reason("TENANT_VALIDATION_FAILED").
    Message("validation failed").
    Details([]commonErrors.FieldError{
        {Field: "inn", Reason: "TENANT_INN_INVALID_LENGTH", Message: "invalid INN length", Params: map[string]any{"expected": 12}},
        {Field: "name", Reason: "TENANT_NAME_REQUIRED", Message: "name required"},
    }).
    Build()
```

`Reason` codes follow `<SERVICE>_<DOMAIN>_<CONDITION>` screaming snake. Reasons emitted by go-common's own middleware use the `COMMON_*` prefix (`COMMON_TOKEN_EXPIRED`, `COMMON_TOKEN_STALE`, `COMMON_SESSION_LOGGED_OUT`, `COMMON_SESSION_REVOKED`, `COMMON_ACCOUNT_SUSPENDED`, `COMMON_AUTH_REQUIRED`, `COMMON_ADMIN_REQUIRED`, `COMMON_REGISTRATION_TOKEN_REQUIRED`, `COMMON_CLIENT_INFO_INVALID`).

`Cause(err)` wraps the underlying Go error so `errors.Is` / `errors.As` work; the cause is never serialized into the wire response.

### `pkg/response`
Standard HTTP response envelope and helpers built on top of `pkg/errors`.

- `response.Success(c, data)` — 200 with `{success: true, data}`.
- `response.Created(c, data)` — 201 with `{success: true, data}`.
- `response.JSONError(c, err)` — 4xx/5xx with `{success: false, error: {code, reason, message, params, details}}`. If `err` is an `*AppError`, all of `Reason`, `Params`, and `Details` are forwarded; otherwise the response is a generic 500 envelope.

The diagnostic log line is written through the **request-scoped logger** (`logger.FromContext(c.Request().Context())`, populated by `middleware.RequestLogger`), so it carries `service`, `request_id`, and `trace_id`/`span_id` — findable by request id and joinable to its trace. The level follows the HTTP **status class**: a 5xx logs at `Error`, a 4xx at `Warn` (seen but not paged, and kept off the `{level="error"}` error-rate panel), and a 2xx/3xx reached defensively is not logged here. The free-text error is preserved in the `error` field and the typed `Reason`, when present, in `reason`. With no request logger on the context, `FromContext` falls back to a logger still carrying the service identity (a no-op when logging was never configured), so the call is always safe.

Wire format for errors:

```json
{
  "success": false,
  "error": {
    "code": 410,
    "reason": "TENANT_ADMIN_TRANSFER_EXPIRED",
    "message": "admin transfer expired",
    "params": {"expiry_time": "2026-04-28T14:00:00Z", "transfer_id": "abc"},
    "details": [
      {"field": "inn", "reason": "TENANT_INN_INVALID_LENGTH", "message": "invalid INN length", "params": {"expected": 12}}
    ]
  }
}
```

Setting `LOG_EMPTY_REASON_WARN=true` makes `JSONError` warn-log every `*AppError` it serializes with an empty `Reason` — useful for catching throw sites that haven't been migrated to the reason-tagged builder.

### `pkg/i18n`
Fs-based overlay localization engine (the 2026-07 one-truth-repo redesign — replaced the old `nicksnyder/go-i18n/v2` bundle wrapper). A bundle holds a compiled-in **en baseline** (a plain `map[string]string` owned by the consuming package, not JSON, not `//go:embed`) plus an optional filesystem **overlay** of per-locale JSON translation files mounted at runtime. `Load`/`Reload` build an immutable `Snapshot` and publish it atomically; consumers always resolve through a `Snapshot`, never through the bundle directly, so a multi-key operation (e.g. a notification's title + body) can't be torn by a concurrent reload.

```go
import (
    "context"

    commoni18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

var baselineEN = map[string]string{
    "tenant.transfer.expired_sms": "Your transfer for {{.OrganizationName}} expired.",
}

// At service boot (main.go): pass the overlay directory PATH (not an fs.FS). An
// empty, missing, or unreadable path degrades to baselines-only — Load never
// errors and never panics. The path is re-stat'd on every Load/Reload, so a
// mount that appears after boot is picked up by the next Reload — no restart.
bundle := commoni18n.NewOverlayBundle(baselineEN, cfg.I18nBundleDir, // "" => baselines-only
    commoni18n.WithNamespace("tenant"),
    commoni18n.WithLogger(log),
)
summary := bundle.Load(ctx) // summary.Source == "overlay" | "baselines-only"

// In the application layer, resolve everything for one response against ONE snapshot:
snap := bundle.Snapshot()
str, err := snap.Resolve("ru", "tenant.transfer.expired_sms", map[string]any{
    "OrganizationName": org.Name,
})
```

- `NewOverlayBundle(baseline, dirPath string, opts...)` — `dirPath` is the overlay **directory path**, not a frozen `fs.FS`. An empty path means baselines-only; a path that is missing, not a directory, or unreadable on `Load` degrades to baselines-only too. Either way construction and loading are infallible — there is no `log.Fatal` path.
- **Late-mount recovery.** The path is re-`os.Stat`'d on **every** `Load`/`Reload` — a ConfigMap overlay mount that appears *after* pod boot is picked up by the next `Reload`, no restart needed. (Binding a frozen `fs.FS` at construction pinned a missing mount forever: a 2026-07 dev incident where a deploy raced apply-configmap by ~3.5 min left both services stuck on English baselines with no way to recover short of a restart. The reload endpoint exists precisely to recover such states.)
- `(b *OverlayBundle) Load(ctx)` / `Reload(ctx)` — both do the same thing (`Reload` just re-reads `dirPath`); return a `*LoadSummary{Namespace, Source, Loaded, Rejected, Missing, Shadowed}` for logging/response, never an error.
- `(b *OverlayBundle) Snapshot() *Snapshot` — the only way to resolve keys; `(s *Snapshot) Resolve(locale, key string, params map[string]any) (string, error)`.
- Resolution order per key: matched-locale overlay -> en overlay -> compiled-in baseline -> `ErrKeyNotFound`. Locale matching is base-language equality (`ru-RU` -> `ru`); an unsupported locale (e.g. `kk` with no `kk.json` mounted) falls through to en, not to a fuzzy-nearest language.
- **Fail-soft per-key validation, not fail-fast.** `WithAllowedParams(func(key string) ([]string, bool))` supplies a placeholder allow-list; a locale value whose `{{.placeholder}}`s aren't a subset of the allowed set is dropped (falls back to baseline) and listed in `LoadSummary.Rejected` as `"<tag>/<key>"`, with a structured warn — it does not fail the load. Without an allow-list, a key defers to its own baseline's placeholders. Malformed JSON or an unparseable locale filename is likewise a skip-with-warn on that one file, never a hard error.
- **En shadow warnings.** If the overlay ships an `en.json` that overrides a baseline key (translators editing the "source of truth" copy instead of just non-en locales), the overlay value wins at `Resolve()` time — the truth repo is authoritative over the compiled-in baseline, by design (spec F3). That key is still accepted-but-flagged: it's listed in `LoadSummary.Shadowed` with a structured warn, because a dev who later changes the Go baseline string won't see their change take effect until the truth repo (`i18n-catalog`) is updated to match — the shadow warning is what surfaces that silent drift.
- `Missing` lists baseline keys absent from the overlay's `en` section — this is the "someone added a new type in Go but forgot the catalog entry" signal (see the new-key flow below).
- `Snapshot`/`OverlayBundle` keep the same sentinel errors as before: `ErrKeyNotFound` (key absent everywhere), `ErrTranslationFailed` (template parse/exec failure, `text/template` with `missingkey=error` so a stray unrendered placeholder never leaks to a user).

**Baseline ownership.** Each consumer owns its own en baseline in Go, next to the code that uses it — there is no more shared `translations/` JSON or `SharedBundle()`. `pkg/notifyrender.BaselineEN` (below) is go-common's only baseline; `notification-service` and `inbox-service` each carry their own. The **truth repo** for translated/overridden strings (ru and any future locale, plus any en override) is the external `i18n-catalog` repo, mounted into each service at `cfg.I18nBundleDir` (default `/etc/i18n`) as a per-namespace directory of `<locale>.json` files via a Kubernetes ConfigMap — that's the directory **path** passed to `NewOverlayBundle`. Nothing in go-common talks to Tolgee or i18n-catalog directly.

### `pkg/notifyrender`
Renders in-app/push notification templates. `notifyrender.Renderer` is constructed by the consuming service from an `i18n.Resolver` (satisfied by `*i18n.OverlayBundle`) the service builds at startup. go-common carries the render *logic* (`typeKey`, `requiredParams`, `optionalParams`, `Render`, `ValidateBundle`) **and** the compiled-in en baseline (`BaselineEN`) — truth-repo overrides layer on top of it at runtime via the overlay engine above.

```go
import (
    commoni18n "github.com/STECH-Super-App/go-common/pkg/i18n"
    "github.com/STECH-Super-App/go-common/pkg/notifyrender"
)

// At service boot (main.go): pass the overlay dir path; empty/missing degrades
// to baselines-only, and a late mount is recovered by the next Reload.
bundle := commoni18n.NewOverlayBundle(notifyrender.BaselineEN, cfg.I18nBundleDir+"/notifyrender",
    commoni18n.WithNamespace("notifyrender"),
    commoni18n.WithLogger(log),
    commoni18n.WithAllowedParams(notifyrender.AllowedParamsByKey), // enforce the catalog contract on overlay values
)
bundle.Load(ctx) // summary logged; never fatal

renderer := notifyrender.NewRenderer(bundle) // NewRenderer takes an i18n.Resolver

// In the application layer:
title, body, err := renderer.Render(notificationType, params, locale)
```

`BaselineEN` is the compiled-in en fallback catalog: dotted `"<section>.title"`/`"<section>.body"` keys for every catalog section, ported from `i18n-catalog/en.json` and kept 1:1 with the `typeKey`/`requiredParams` contract (`baseline_test.go` asserts the union both ways). `AllowedParamsByKey(key string) ([]string, bool)` derives the placeholder allow-list for a catalog key from that type's declared params, for wiring into `i18n.WithAllowedParams`.

**Two param tiers.** `requiredParams` is the producer-side contract: `notifyoutbox` rejects a directive whose required param is empty (there, empty means missing), and `Render` returns `ErrMissingParam` if the key is absent. `optionalParams` is the weaker tier for fields that may legitimately arrive empty. It began as `request_no`, the human-facing delivery request number (Д-13), which rides on 9 of the 18 `SendDelivery*` payloads but is empty on directives emitted before numbering shipped; since the parts catalog landed it also covers **nine parts types** — `product_name` (an unmatched позиция has no card to take a name from, so the producer sends `""` rather than echoing the артикул), `price` / `price_from` (a позиция with no ladder rung has no price), `remaining_causes` (Р56·В-54's conditional lift text, where an EMPTY list is the *good* case) and `new_address_count` (Р31's second-edition tail). The repair family widens the tier once more: `request_no` on all 23 deal types (order-service stamps the human-facing number, and a directive may precede it), `machinery` and `price` where the sentence survives without them, `provider_name` on the expiry text (each `had_offer` arm names the responder only if there is one to name), `distance_km` through `countOrEmpty`, `posting_no` on the four **customer-addressed** board types (`RESPONSE_RECEIVED`, `RESPONSE_WITHDRAWN`, `POSTING_EXPIRING`, `POSTING_EXPIRED_CUSTOMER` — «Заявка #N», the customer's own posting number; the four responder-addressed board rows carry none by §5 R7, and it is **not** `request_no`, which is order-service's deal sequence), and the four **branch discriminators** `had_offer` / `deal_completed` / `cancelled_by` / `supersedes_previous`, which are optional for a structural reason worth stating: they are never printed, they pick between editions of a sentence, and a REQUIRED param must appear as a literal `{{.name}}` — so a discriminator can only live in the weaker tier. Templates read them as `{{if eq .x "…"}}` on **both** arms, never a bare `{{else}}` carrying the opposite edition, so an absent or unrecognised value lights neither branch and the reader is told less rather than something false (`repair_request_cancelled`'s third `{{else}}` arm prints the actor with no role noun for exactly that reason). Two non-repair types joined this sub-tier: `ORDER_CANCELLED` reads `cancelled_by` (`customer` → «by the renter», `provider` → «by the owner») and `decline_reason` (the renter's counter-offer decline, `PRICE` | `DATES` | `TERMS` | `FOUND_ANOTHER` | `OTHER`, one clause each; empty on every other cancel) with explicit arms only, so an unknown token drops the agent or the clause; `DELIVERY_REQUEST_CANCELLED` reads `cancelled_by` (`customer` | `carrier`) to pick the subject noun and — because the token governs the WHOLE sentence — keeps a neutral default arm that names no side («Request #N was cancelled.»), never the raw token. `supersedes_previous` is the single-armed exception and is allowed to be: its false edition is «say nothing», which is true under every value the wire can carry. `deal_completed` on `REPAIR_REVIEW_WINDOW_ENDING` is the **second** exception and is written `{{if eq .deal_completed "false"}}…{{else}}…{{end}}` deliberately (repair critique NOTIF-04): there the discriminator governs the WHOLE sentence rather than adding a suffix, so two explicit arms with no default would render an **empty body** for an absent value. Only the `"false"` arm asserts anything, and the `{{else}}` is byte-identical to the text that shipped before the field existed — the rule's purpose (never assert what the wire did not say) is kept; only its shape is not. The same field on `REPAIR_REVIEW_RECEIVED` keeps the ordinary two-armed form, because there it only appends a clause. Rules for an optional param:

- The baseline **must** guard it: `{{if .request_no}} #{{.request_no}}{{end}}`, so an empty value renders the plain sentence instead of a dangling `#`. `TestBaselineGuardsOptionalParams` enforces the guard; `TestRenderDelivery_RequestNumber` pins the exact rendered string in all three states (populated / empty / key absent).
- `Render` fills an **absent** optional key with `""` before templating — the resolver runs `missingkey=error`, which trips on a missing key even inside an `{{if}}`, so without the fill a legacy caller would get a 500 instead of the fallback text.
- Never promote one to `requiredParams` to "make it render": that makes every not-yet-numbered directive fail to publish.
- A numeric optional param must be formatted to `""` and not `"0"` — Go's template truth test runs on the STRING, so `"0"` is non-empty and its `{{if}}` guard is always true. `countOrEmpty` does that; a **required** count must use `strconv.Itoa` instead, because there empty means missing.

`ExtractParams` is a hand-written type switch over the envelope's payload oneof, so a new payload **field** with no line added to its arm would simply never reach a template. `extract_completeness_test.go` is the guard: it walks the oneof through protoreflect and requires every declared string field to surface as a param, exempting `*_id` by a documented family rule (no template reads an id — ids ride in `deep_link.params`) and everything else only through the explicit `notTemplated` / `unmappedByDesign` maps, each entry carrying its reason and checked in both directions, so an exemption that stops being true fails the build.

Package-level helpers: `ExtractParams`, `RequiredParams`, `OptionalParams`, `IsVerbatim`, `RenderVerbatim`, and the `Reason*` error constants / constructors (`ErrUnknownType`, `ErrMissingParam`, `ErrEmptyPayload`, `ErrEmptyVerbatimText`).

`ValidateBundle(fs.FS) error` is the placeholder-consistency guard that `i18n-catalog`'s `cmd/validate` runs on every PR: it checks that every `{{.placeholder}}` in the source-locale `en.json` is declared for its type (required **or** optional), and every declared `requiredParam` appears in at least one placeholder. Optional params are exempt from that reverse check — a translation may drop the request number from a sentence. A partial bundle (subset of catalog types) is accepted; `ValidateBundleComplete(fs.FS) error` additionally asserts every catalog `NotificationType` has a section present — that's the one `i18n-catalog` CI runs against the real shipped bundle.

**New-key / new-type flow.** Adding a translated string to an *existing* notification type is just an `i18n-catalog` PR (new locale entry under an existing section) — no go-common change needed. Adding a **new in-app notification type** is three coupled steps, strictly ordered (this is rule **F11** from the i18n redesign spec):
1. A go-common PR adds the proto enum pin, the `typeKey`/`requiredParams` entry in `pkg/notifyrender/catalog.go`, and the new `BaselineEN` entry — merges and gets tagged first.
2. The `i18n-catalog` PR carries **both** the new `en` section **and** `go get github.com/STECH-Super-App/go-common@<the new sha>` in the same commit. `i18n-catalog`'s validator runs `ValidateBundleComplete` against its **pinned** go-common version, so a catalog PR that adds the en section without bumping the pin fails with "has no matching NotificationType" — a real incident (2026-07-13, `team_member_left`) that looked like unrelated CI breakage. Skipping the bump is soft at runtime (the compiled-in baseline still covers en; only non-en locales are affected until the catalog PR lands) but hard-red in CI.
3. Consumer services (notification-service, inbox-service) re-pin go-common and redeploy to pick up the new baseline entry and render the new type.
The `Missing` field on `LoadSummary` is the load-time backstop for this flow: a baseline key with no matching overlay `en` entry means step 2 was skipped or hasn't merged yet.

### `pkg/notifyoutbox`
The one publishing door for `TOPIC_NOTIFICATION_EVENTS`. `PublishDirective(ctx, pub, tx, env)` validates the envelope and then writes the outbox row through the caller's own `outbox.Tx`; direct `outbox.Publisher.PublishProto` to that topic is forbidden. Every rejection is a typed `*errors.AppError` with a `NOTIFYOUTBOX_*` Reason, and **every rejection happens inside the producer's transaction** — the deliberate consequence is that a malformed directive rolls the producer's own domain write back instead of being published as an undeliverable push. Adding a check here is therefore never cosmetic: it is a new way for a business write to fail.

Three tiers of check: envelope shape (`event_id`, `aggregate_id`, `occurred_at`, a specified type, non-empty channels, exactly-one-of `recipient_user_id`/`recipient_tenant_id` where a recipient is required, a deep link when IN_APP is present, a non-nil payload), the per-family channel rules, and the catalog's required-param coverage (IN_APP only — an EMAIL/SMS-only directive uses notification-service's own template contract; a verbatim `PLATFORM_MESSAGE` is asserted non-empty instead).

**The repair channel rule** (`NOTIFYOUTBOX_REPAIR_CHANNEL_SET`). A `NOTIFICATION_TYPE_REPAIR_*` directive must declare exactly `[CHANNEL_IN_APP, CHANNEL_PUSH]` — both, in either order, and nothing else (spec §11, D28). It is recognised by enum-name prefix, not by a list of the 31 values, so the thirty-second repair type is guarded the day proto-contracts mints it. The rule exists because the guard everyone cited was not one: "repair has no rows in notification-service's email/SMS template table" describes a table the dispatcher consults **only for a type it does not recognise**, and it recognises every repair type — an envelope declaring EMAIL would have reached the email sender with the push title and body in it (repair critique 2026-09-23, NOTIF-09). The real guard had been that both producers hard-code the pair, which is a convention, one edit from being untrue. Widening repair to a third channel is a deliberate change to this function, plus the email/SMS baselines that would then have to exist. No other family is touched: rent, delivery and parts directives legitimately take EMAIL and SMS.

⚠ **The prefix is the whole membership test, so the rule is family-wide and cannot be narrowed by adding a type.** A future `NOTIFICATION_TYPE_REPAIR_*` that legitimately needs EMAIL — parts is the precedent: it shipped in-app-only and is not fenced at all, so nothing stopped it widening later — will be refused by this guard the moment it is minted in proto-contracts, and the refusal lands INSIDE the producer's transaction, which rolls that producer's business write back. It will not fail in review or at boot; it will fail on the first directive, in production, as a rolled-back write. That is the deliberate trade (a forgotten repair type is guarded by default, which is the failure mode worth having), but it means widening repair is a TWO-repo change with an ordering: `go-common` must carry the exemption — a second predicate beside `isRepairType`, never a hand-maintained list of the other 31 — and be merged and pinned by both producers BEFORE the new type is emitted. Do not solve it by deleting the prefix check.

### `pkg/logger`
Structured logging using `uber-go/zap`. **JSON encoder unconditionally** — a container's log stream is not a developer's terminal, and a console line is unparseable by Loki's `| json`, which would take out the `level` label, the `request_id` filter and the `trace_id`→Tempo join in one stroke.

- `New(level, service)` — returns a configured `*zap.Logger` tagged with `service`, for application code that owns a logger instance. Pass a literal equal to the workload's scrape identity (`"order-service"`, `"api-gateway"`); the same string must be the Prometheus `job` and the Loki stream label, or a metrics panel and a log stream cannot be pivoted between. Sampling is off, so access lines are never silently dropped under load.
- `IntoContext(ctx, l) context.Context` / `FromContext(ctx) *zap.Logger` — the request-scoped logging spine. `middleware.RequestLogger` stores a child logger carrying `request_id` (+ `trace_id`/`span_id`); every layer below reads it with `FromContext(ctx)`. The signature is `context.Context`, not `echo.Context`, because repositories, gRPC clients and Kafka consumers only ever hold the former. `FromContext` never returns nil: it falls back to the last logger built by `New`, then to a no-op.
- Package-level `Warn(msg, fields...)`, `Info(msg, fields...)`, `Error(msg, fields...)` — for cross-cutting library code in go-common that emits diagnostics without holding a logger of its own. Backed by a lazily-initialized package logger driven by `LOG_LEVEL`.
- `String(key, value)`, `Int(key, value)` — field constructors re-exported from zap so callers don't need to import zap directly.

Application code: prefer `New(level, service)` for an injected logger, and `FromContext(ctx)` on the request path. Library code in go-common: use the package-level helpers.

### `pkg/middleware`
Common HTTP middleware.

Observability trio — wire in this order (`Tracing` → `RequestLogger` → `Metrics`), so the logger sees the span and the histogram measures the handler:
- `Tracing()`: server span per request, named `METHOD route-template` (`unmatched` when the router matched nothing), extracts the inbound W3C `traceparent`, records `http.status_code`. Costs nothing when `tracing.Init` found no endpoint.
- `RequestLogger(base, opts...)`: builds the request-scoped child logger (`request_id` from `X-Request-ID`, plus `trace_id`/`span_id` when a span is active), stores it on `c.Request().Context()`, and emits **exactly one** INFO access line per request (`route`, `method`, `status`, `duration_ms`). `WithUpstream(fn)` adds the gateway-only `upstream` field. It **replaces** `Logger` at call sites — running both doubles log volume.
- `Metrics()`: observes `http_request_duration_seconds{method, route, status_class}`. Native Echo shape, never `WrapMiddleware`: `c.Path()` exists only on the Echo context, and wrapping `http.ResponseWriter` would drop `http.Flusher` and break the SSE endpoints.
- `MetricsUnaryServerInterceptor()`: observes `grpc_server_handling_seconds{grpc_service, grpc_method, grpc_code}`. Attach with `grpc.ChainUnaryInterceptor`, never `grpc.UnaryInterceptor` (grpc-go allows the latter only once per server).

All three skip the operational routes `/metrics`, `/health`, `/livez`, `/readyz`, and clamp an unmatched route to `unmatched` — otherwise probe traffic and vulnerability scanners mint series and log lines forever.

- `Logger`: **deprecated** — logs HTTP requests with no `request_id`, so its lines cannot be correlated across the gateway hop. Use `RequestLogger`.
- `CORS`: Handles Cross-Origin Resource Sharing.
- `AuthMiddleware`, `OptionalAuthMiddleware`, `RegistrationAuthMiddleware`, `AdminMiddleware`, `ClientMiddleware`: emit `COMMON_*` reasons via `pkg/errors` + `pkg/response` on rejection.
- `ParseUUIDParam(c, paramName, reason)`: validates an Echo path parameter with `input.UUID` — only the 36-character hyphenated form (any case) is accepted; braces, `urn:uuid:` and bare 32-hex are refused — and returns it **lower-cased**. On failure returns a 400 `*AppError` with the supplied reason, a message derived from `paramName`, and the `*input.Failure` as its cause. Stops malformed UUIDs at the handler boundary so they never reach the repository layer (where Postgres would reject them with SQLSTATE 22P02 and leak as a 500).

### `pkg/metrics`
Prometheus instruments and exposition. `Registry` is the **only** registry anything serves — never `promauto` or the prometheus default registerer, which nothing scrapes.

- `MountOn(e *echo.Echo)`: wires `GET /metrics` on an Echo instance. Idempotent — safe across two Echo instances, and safe to call twice on one.
- `StartServer(addr) (stop func(context.Context) error)`: standalone `net/http` listener for services with no public Echo, and the uniform mechanism when metrics live on their own port.
- `HTTPRequestDuration`, `GRPCServerHandling`: the two labelled families, **registered at package init** — never inside a middleware factory or a server constructor, because service tests rebuild those repeatedly in one process and `MustRegister` panics on the second call.
- `DefaultBuckets` (`0.005 … 10`), `StatusClass(code)`, `RouteUnmatched`: the shared label contract. Allowed label keys fleet-wide: `method, route, status_class, grpc_service, grpc_method, grpc_code, topic, group, reason, vertical, sqlstate` (`sqlstate` is `pkg/db/pgerr`'s closed five-value set). Never ids, raw paths, emails or tokens.
- `NewCounter`, `NewGauge`, `NewHistogram`: constructors for **unlabelled** instruments. Labelled families are built with `prometheus.New*Vec` directly.

### `pkg/tracing`
OpenTelemetry tracing setup — one call configures the process.

- `Init(serviceName) (shutdown func(context.Context) error, err error)`: sets the global tracer provider and the W3C propagator.

With `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) **unset it installs a no-op provider and returns a nil error** — a service must boot identically with no Tempo present. The propagator is installed either way, because propagation is independent of exporting. Sampling honours `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG`, defaulting to `parentbased_traceidratio` at `1.0`. The exporter dials lazily, so an unreachable collector drops spans instead of failing requests. `shutdown` is never nil.

```go
shutdown, err := tracing.Init("order-service")
if err != nil {
    return err
}
defer func() { _ = shutdown(context.Background()) }()
```

Span names obey the same cardinality law as metric labels: route templates and topic names, never raw URLs or ids.

### `pkg/lifecycle`
The fleet's process shutdown orchestrator: SIGTERM becomes an ordered, bounded drain instead of a runtime kill. It **accepts already-built components** — it builds nothing, reads no config and never calls `os.Exit`/`Fatal`; `main` keeps the wiring and the exit code. Design: dev-setup `docs/superpowers/specs/2026-10-03-go-graceful-shutdown-design.md`.

- `SignalContext(parent) (ctx, cancel)`: cancelled on the first **SIGINT or SIGTERM** (Docker and the kubelet send SIGTERM — `os.Interrupt` alone is not enough). After the first signal the handler unregisters, so a second signal kills the process immediately.
- `New(log, opts...)`, `WithShutdownBudget(d)` (default `DefaultShutdownBudget` = 25 s, one deadline for every phase, fits the k8s 30 s grace), `WithDrainDelay(d)` (default 0, counts against the budget).
- Registration: `HTTP(name, *echo.Echo, addr)`, `GRPC(name, *grpc.Server, net.Listener)`, `Server(name, start, stop)` (escape hatch, no hard stop), `Worker(name, run func(ctx) error)`, `Closer(name, close func(ctx) error)`, `CloserGroup(name, members ...NamedCloser)` (`NamedCloser{Name, Close func(ctx) error}`). `Stopping() <-chan struct{}` closes when shutdown begins — SSE/WebSocket handlers select on it.
- `Run(ctx) error`: starts everything, waits for the trigger — `ctx` done (a signal), **any** server start returning (`http.ErrServerClosed` included), or a worker returning a non-nil error (a worker returning `nil` early is only logged) — then, inside the budget:
  0. `Stopping()` closes, the drain delay elapses;
  1. servers stop **in parallel** (`e.Shutdown` / `GracefulStop`); one still busy at the deadline is hard-stopped (`e.Close` / `Stop`) and logged at Warn;
  2. the **workers' context is cancelled only now** — it derives from `context.WithoutCancel(ctx)`, not from the signal, so consumers and the outbox relay keep running while requests drain — and Run waits for them;
  3. closers run **sequentially in registration order** (not reverse; the order is written in `main`). Recommended: Kafka readers/writers → Redis → DB pool → metrics server → tracer flush. A closer that no longer fits the budget is skipped and reported, never run concurrently with the next. A **`CloserGroup` is one step of that order whose members run concurrently**: the step ends when every member has finished or the budget is spent, and each member is reported on its own as `closer:<group>/<member>` (`ErrCloserFailed`, `ErrPanic`, timeout) without stopping its siblings.

  **Register every Kafka reader and writer in one `CloserGroup("kafka", …)`.** A kafka-go consumer-group `Reader.Close` waits for each partition's in-flight Fetch, a broker long-poll of up to `ReaderConfig.MaxWait` (default 10 s) whose socket read deadline is that same `MaxWait`; cancellation is only seen between fetches. An idle reader therefore takes up to one `MaxWait` to close even after its consumer stopped (measured 7.5–9 s on the local stack), and sequential closers sum it: two readers cost 15.5 s of the 25 s budget, three would overrun it. Grouped, they cost one `MaxWait`. Lowering a reader's own `MaxWait` is the only way to shorten that one wait, at the price of more fetch round trips on an idle topic.

  Every goroutine recovers panics (logged with stack, reported as `ErrPanic`). Run logs `shutdown complete` (`trigger`, `duration`, `timed_out`) and returns **nil for a clean signal-triggered shutdown**, otherwise `errors.Join` of the trigger failure (`ErrServerExited` / `ErrWorkerFailed`), timeouts (`ErrShutdownTimeout`), closer errors (`ErrCloserFailed`) and worker failures during shutdown. Run may be called once; registering after it panics.

```go
func main() {
    ctx, stop := lifecycle.SignalContext(context.Background())
    defer stop()
    // ... build log, tracing, pool, reader, kafkaWriter, ob (outbox), e (echo), grpcServer — start-up Fatal is fine here ...
    stopMetrics := metrics.StartServer(":9091")

    app := lifecycle.New(log)
    app.HTTP("http", e, ":8080")
    app.GRPC("grpc", grpcServer, grpcListener)
    app.Worker("user-events-consumer", dispatcher.Run)
    app.Worker("outbox", ob.Run)
    app.CloserGroup("kafka",
        lifecycle.NamedCloser{Name: "user-events-reader", Close: func(context.Context) error { return reader.Close() }},
        lifecycle.NamedCloser{Name: "writer", Close: func(context.Context) error { return kafkaWriter.Close() }},
    )
    app.Closer("postgres", func(context.Context) error { pool.Close(); return nil })
    app.Closer("metrics", stopMetrics)
    app.Closer("tracing", shutdownTracing)

    if err := app.Run(ctx); err != nil {
        os.Exit(1) // Run already logged "shutdown complete" with the cause
    }
}
```

No `signal.Notify`, no shutdown-path `Fatal`, no defer-ordered teardown in a migrated `main`.

### `pkg/utils`
Generic utility functions.
- **Ptr**: Pointer helpers (`Ptr[T]`, `ToVal[T]`).
- **Slice**: Slice manipulation (`Contains`, `Map`, `Filter`).

### `pkg/money`
The platform's sanctioned money primitive: an immutable `Money` value holding an integer amount in **minor units** (kopecks for RUB) paired with a validated ISO 4217 `Code`. Floats are forbidden for money across STECH — this is the only approved representation.
- `ParseCode(s)` / `New(amountMinor, code)`: `Code` is three uppercase ASCII letters; **currency-registry membership is deliberately NOT validated** — which currencies a service accepts is that service's own policy (e.g. order-service whitelists RUB only).
- `Add`, `Mul`, `Cmp`: overflow-checked arithmetic and comparison; cross-currency operands and int64 overflow return typed `pkg/errors` `AppError`s (`COMMON_MONEY_*` reasons, HTTP 422).
- Scope is deliberately minimal: **no FX/conversion, no rounding, no formatting/`String()`** (YAGNI).

## Events pipeline

Async events across STECH services flow through three cooperating packages — **`pkg/envelope`** (wire contract), **`pkg/outbox`** (producer side + dedup state), **`pkg/events`** (consumer side). The split is one-directional: envelope imports nothing repo-local; outbox and events both import envelope; outbox and events never import each other.

```
          ┌─────────────────────────────────────┐
          │   pkg/envelope (Kafka wire contract) │
          │   headers, Headers, Extract*, enums │
          └──────────▲───────────────▲──────────┘
                     │               │
          ┌──────────┴─────┐  ┌──────┴──────────┐
          │   pkg/outbox   │  │    pkg/events   │
          │ (producer +    │  │ (typed consumer │
          │ dedup state)   │  │  dispatcher)    │
          └────────────────┘  └─────────────────┘
```

### `pkg/envelope`
Single source of truth for the Kafka message envelope carried on every event.

- **Header constants**: `HeaderEventID`, `HeaderEventType`, `HeaderAggregateType`, `HeaderAggregateID`, `HeaderOccurredAt`, `HeaderSchemaVersion`, `HeaderContentType`, `HeaderRetryCount`, `HeaderTraceparent`, plus legacy `HeaderOutboxID` for rolling-migration compat.
- **`traceparent`** carries W3C trace context across the async hop, so one trace spans request → outbox → relay → consumer. It is injected by `PublishProto` from the **producing request's** context and read back by the Dispatcher as a remote parent. No migration was needed: the outbox row already had `headers JSONB`, and the relay copies every entry verbatim.
- **Value constants**: `ContentTypeProtoJSON`, `SchemaVersionV1`.
- **`Headers` type** (`map[string]string`) with typed accessors: `EventID()`, `EventType()`, `AggregateType()`, `AggregateID()`, `OccurredAt() (time.Time, error)`, `SchemaVersion()`, `ContentType()`, `RetryCount() int`, `Traceparent()`.
- **`FromKafka([]kafka.Header) Headers`** — build typed view from raw Kafka headers.
- **Extractors**: `ExtractEventID` (reads `event_id`, falls back to legacy `outbox_id`), `ExtractOutboxID` (deprecated alias), `ExtractEventType`.

### `pkg/outbox`
Transactional Outbox Pattern for guaranteed at-least-once event delivery to Kafka, plus consumer-side idempotency.

- `New(pool, kafkaWriter, logger, cfg)`: Creates the full outbox subsystem.
- `Start(ctx)`: Launches three background goroutines — relay (poll → Kafka), reaper (cleanup) and the metrics sampler — and returns one `stop()` that shuts all three down. There is no separate `StartMetrics` a repo could forget to call.
- `Run(ctx) error`: `Start` for a blocking caller — blocks until `ctx` is cancelled, stops all three, returns `ctx.Err()`. This is the shape `pkg/lifecycle` registers: `app.Worker("outbox", ob.Run)`.
- **Shutdown flush.** `FetchPending` runs on the relay's cancellable context, so no new batch is taken after a cancel; but a batch **already fetched** is written to Kafka and marked sent (both the full-success and the partial `kafka.WriteErrors` arms) on a context detached from that cancellation and cancelled `RelayConfig.ShutdownFlushTimeout` (default 5 s) **after** it — a SIGTERM mid-batch no longer leaves delivered rows pending to be re-published on restart. The bound starts at cancellation, not at fetch: a write slower than the timeout during normal operation is not cut short. A zero/negative value means the default.
- `Store.PendingStats(ctx) (count int64, oldestAgeSeconds float64, err error)`: the single-round-trip aggregate behind the backlog gauges.
- `Migrate(postgresURL)`: Runs the embedded goose migrations (outbox + dedup tables).
- `RunInTx(ctx, pool, fn)`: Executes a function within a Postgres transaction.

**Publishing:**
- `Publisher.PublishProto(ctx, tx, opts)` — **preferred**. Typed proto message path. Serializes via `protojson` (snake_case), auto-injects the full envelope header set (`event_id`, `event_type`, `aggregate_type`, `aggregate_id`, `occurred_at`, `schema_version`, `content_type`). Generates `event_id` UUID and writes it to both the header and the proto payload's `event_id` field.
- `Publisher.Publish(ctx, tx, opts)` — legacy any-typed JSON path. Retained for transitional compat during the events-to-proto rollout; removed in the cleanup PR.

**Consumer-side idempotency:**
- `Deduplicator.Process(ctx, eventID, fn)` — atomic check-and-record-and-run in a Postgres transaction with `SELECT ... FOR UPDATE`. If the event_id is already recorded, `fn` is not invoked and the call returns nil. If `fn` errors, the event_id is not recorded (safe to retry). Provides exactly-once processing on top of at-least-once Kafka delivery.

**Environment Variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `OUTBOX_POLL_INTERVAL` | `1s` | Relay polling frequency |
| `OUTBOX_BATCH_SIZE` | `100` | Messages per poll cycle |
| `OUTBOX_SHUTDOWN_FLUSH_TIMEOUT` | `5s` | How long an already-fetched batch may keep writing + marking sent after the relay is cancelled |
| `OUTBOX_REAPER_INTERVAL` | `5m` | Cleanup schedule |
| `OUTBOX_RETENTION` | `72h` | Sent message retention before deletion |
| `OUTBOX_METRICS_INTERVAL` | `15s` | Backlog gauge sampling (deliberately far slower than the poll interval) |

**Metrics** (on `metrics.Registry`, all unlabelled so one panel covers both the Go fleet and sale-service's PHP relay):

| Metric | Type | Meaning |
|---|---|---|
| `outbox_pending_messages` | gauge | Rows in `status='pending'` |
| `outbox_oldest_pending_age_seconds` | gauge | Age of the oldest pending row — **the** stall signal, immune to load bursts |
| `outbox_last_success_timestamp_seconds` | gauge | Last error-free relay poll; pairs with `up` so a dead relay goroutine is visible despite frozen gauges |
| `outbox_relayed_events_total` | counter | Rows forwarded to Kafka and marked sent |
| `outbox_relay_errors_total` | counter | Relay poll cycles that ended in an error |
| `outbox_reaped_events_total` | counter | Sent rows deleted after retention |

Both gauges are published **from process start, including on an empty table** (`MIN(created_at)` over zero rows is SQL NULL → reported as `0`): a series that only appears once a row exists makes an alert unevaluable exactly while a service is idle. The freshness gauge is set at relay start and on every error-free poll **including zero-row cycles** — "success" means `err == nil`, not `processed > 0`.

**Quick Start (producer):**
```go
import (
    eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
    usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
    "github.com/STECH-Super-App/go-common/pkg/events"
    "github.com/STECH-Super-App/go-common/pkg/outbox"
)

// In main.go — after existing migrations
outbox.Migrate(cfg.Postgres.URL)
ob := outbox.New(pool, kafkaWriter, zlog, outbox.DefaultConfig(), defaultTopic)
app.Worker("outbox", ob.Run) // pkg/lifecycle; or: stop := ob.Start(ctx); defer stop()

// In a use case — atomic domain write + event publish
outbox.RunInTx(ctx, pool, func(tx outbox.Tx) error {
    if err := repo.WithTx(tx).Create(ctx, entity); err != nil {
        return err
    }
    return ob.Publisher.PublishProto(ctx, tx, outbox.PublishProtoOptions{
        AggregateType: "user",
        AggregateID:   entity.ID,
        Topic:         events.TopicName(eventsv1.Topic_TOPIC_USER_EVENTS),
        Message: &usersv1.UserRegistered{
            UserId: entity.ID, Phone: entity.Phone, Name: entity.Name,
        },
    })
})
```

### `pkg/events`
Typed consumer dispatcher. Registers proto-typed handlers keyed on the proto FQN (`event_type` header), routes each Kafka message via `protojson` decode, and handles retry / DLQ / dedup.

- `NewDispatcher(reader, dlq, opts ...)` — DLQ is a **required** positional arg; no code path drops a failed message. Options: `WithRetry(w)`, `WithDedup(d)` (takes any value satisfying `Process(ctx, id, fn) error` — typically `*outbox.Deduplicator`), `WithMaxRetries(n)` (default 3), `WithLogger(l)`, `WithGroup(id)`.
- `WithGroup(id)` — the consumer group id used as the `group` label on every consumer metric. Pass the Kafka `GroupID` **verbatim**, exactly the string the `kafka.ReaderConfig` got: only that value matches kafka-exporter's `consumergroup` label, which is what lets a lag panel and a dead-letter counter sit on the same dashboard row. Do not pass the DLQ short name — a repo can have both and they differ (`order-review-events-consumer` vs `order`). Unset means `GroupUnknown` (`"unknown"`).
- `Handle[T proto.Message](d *Dispatcher, fn func(ctx, T) error)` — register a typed handler. Routing key is derived from `proto.MessageName(*new(T))`; protojson-unmarshal into a fresh `T` per message.
- `d.Run(ctx) error` — poll loop; returns `ctx.Err()` on cancel and **`nil` once the reader is closed** (kafka-go answers a closed reader with `io.EOF` — before, `Run` busy-looped on it). Any other fetch error is retried with a capped exponential backoff (100 ms doubling to 5 s, ctx-aware, reset by a successful fetch), so a broker outage is not a hot loop. **A cancel finishes the message in flight:** `ctx` governs only fetching — it is checked before every fetch (kafka-go may hand out a buffered message even on a cancelled ctx) and passed to `FetchMessage` — while an already-fetched message's handler, dedup transaction, retry/DLQ write and offset commit run on `context.WithoutCancel(ctx)` (values and trace kept), so a shutdown never fails the steps that record a side effect the handler already performed. No extra timeout: the clients' own timeouts and lifecycle's budget bound it. Register it directly as a `pkg/lifecycle` worker: `app.Worker("…-consumer", disp.Run)` — no draining wrapper needed.
- `TopicName(eventsv1.Topic) string` — converts the proto `Topic` enum to its wire name (e.g. `TOPIC_USER_EVENTS` → `"user-events"`).
- `ErrPoisonPill` — sentinel for non-retryable handler errors (wrap with `%w`; goes straight to DLQ without consuming retry budget).

**Failure classification (→ DLQ with `x-dlq-reason`):**
- `ErrPoisonPill` → `poison_pill`
- `protojson.Unmarshal` failure → `unmarshal_error`
- Handler panic → `handler_panic` (panic is recovered and converted to an error)
- Any other error → `max_retries` after the retry budget is exhausted; forwarded to retry topic (if configured) otherwise

**Every DLQ message carries:** `x-dlq-reason`, `x-dlq-error` (truncated), `x-dlq-first-seen-at`, `x-dlq-last-seen-at`, plus the original envelope.

**Metrics** (on `metrics.Registry`; `topic` is read per message off `kafka.Message.Topic`, since one dispatcher legitimately spans a base topic and its retry tier):

| Metric | Labels | Meaning |
|---|---|---|
| `events_consumer_processed_total` | `topic`, `group` | Handler ran and the offset was committed |
| `events_consumer_handler_failures_total` | `topic`, `group` | Handler errored, panicked, or the payload failed to unmarshal |
| `events_consumer_retried_total` | `topic`, `group` | Forwarded to the retry topic |
| `events_consumer_dead_lettered_total` | `topic`, `group`, `reason` | Written to the DLQ, by failure reason |
| `events_consumer_dedup_hits_total` | `topic`, `group` | Redelivery skipped because the `event_id` was already processed |
| `events_consumer_failurepath_errors_total` | `topic`, `group` | **The DLQ/retry write itself failed** and the offset was left uncommitted — the group is wedged, not merely poisoned |

**Tracing + logging:** each message is handled inside a consumer span that continues the producer's trace (extracted from the `traceparent` envelope header as a **remote parent**), and the dispatcher's log lines carry `event_id`, `topic` and — when a span is active — `trace_id`/`span_id`. The same child logger is put on the handler's context, so `logger.FromContext(ctx)` works on the consumer path exactly as it does on the HTTP path.

**Quick Start (consumer):**
```go
import (
    usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
    tenantsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/tenants/v1"
    eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
    "github.com/STECH-Super-App/go-common/pkg/events"
    "github.com/STECH-Super-App/go-common/pkg/outbox"
)

reader := kafka.NewReader(/*...*/)
// DLQ names come from the registry helpers — never hardcode a topic string.
dlqWriter := &kafka.Writer{Topic: events.DLQName(eventsv1.Topic_TOPIC_USER_EVENTS, "auth"), /*...*/}
dedup := outbox.NewDeduplicator(pool)

disp := events.NewDispatcher(reader, dlqWriter, events.WithDedup(dedup))

events.Handle(disp, func(ctx context.Context, e *usersv1.UserUpdated) error {
    return sessionService.RevokeAllForUser(ctx, e.UserId)
})
events.Handle(disp, func(ctx context.Context, e *tenantsv1.TenantSuspended) error {
    return sessionService.RevokeAllForTenant(ctx, e.TenantId)
})

return disp.Run(ctx)
```

## Commit message prefixes

This project uses common commit message prefixes inspired by Conventional Commits.

- **feat** – New feature or functionality.
- **fix** – Bug fix or defect correction.
- **docs** – Documentation-only changes (README, comments, etc.).
- **style** – Changes that do not affect code meaning (formatting, linting).
- **refactor** – Code changes that neither fix a bug nor add a feature.
- **perf** – Changes that improve performance.
- **test** – Adding or updating tests.
- **build** – Changes to build system or external dependencies.
- **ci** – Changes to CI/CD configuration.
- **chore** – Maintenance tasks that do not modify app behavior.
- **revert** – Revert of a previous commit.

**Format:** `type: short summary`, for example:

```text
feat: add user search endpoint
fix: handle nil pointer in auth middleware
```
