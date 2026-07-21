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
- `response.JSONError(c, err)` — 4xx/5xx with `{success: false, error: {code, reason, message, params, details}}`. If `err` is an `*AppError`, all of `Reason`, `Params`, and `Details` are forwarded; otherwise the response is a generic 500 envelope and the error is logged.

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
Renders in-app/push notification templates. `notifyrender.Renderer` is constructed by the consuming service from an `i18n.Resolver` (satisfied by `*i18n.OverlayBundle`) the service builds at startup. go-common carries the render *logic* (`typeKey`, `requiredParams`, `Render`, `ValidateBundle`) **and** the compiled-in en baseline (`BaselineEN`) — truth-repo overrides layer on top of it at runtime via the overlay engine above.

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

`BaselineEN` is the compiled-in en fallback catalog: dotted `"<section>.title"`/`"<section>.body"` keys for every catalog section, ported from `i18n-catalog/en.json` and kept 1:1 with the `typeKey`/`requiredParams` contract (`baseline_test.go` asserts the union both ways). `AllowedParamsByKey(key string) ([]string, bool)` derives the placeholder allow-list for a catalog key from `requiredParams`, for wiring into `i18n.WithAllowedParams`.

Package-level helpers that stay unchanged: `ExtractParams`, `RequiredParams`, `IsVerbatim`, `RenderVerbatim`, and the `Reason*` error constants / constructors (`ErrUnknownType`, `ErrMissingParam`, `ErrEmptyPayload`, `ErrEmptyVerbatimText`).

`ValidateBundle(fs.FS) error` is the placeholder-consistency guard that `i18n-catalog`'s `cmd/validate` runs on every PR: it checks that every `{{.placeholder}}` in the source-locale `en.json` is declared in `requiredParams` for its type, and every declared `requiredParam` appears in at least one placeholder. A partial bundle (subset of catalog types) is accepted; `ValidateBundleComplete(fs.FS) error` additionally asserts every catalog `NotificationType` has a section present — that's the one `i18n-catalog` CI runs against the real shipped bundle.

**New-key / new-type flow.** Adding a translated string to an *existing* notification type is just an `i18n-catalog` PR (new locale entry under an existing section) — no go-common change needed. Adding a **new in-app notification type** is three coupled steps, strictly ordered (this is rule **F11** from the i18n redesign spec):
1. A go-common PR adds the proto enum pin, the `typeKey`/`requiredParams` entry in `pkg/notifyrender/catalog.go`, and the new `BaselineEN` entry — merges and gets tagged first.
2. The `i18n-catalog` PR carries **both** the new `en` section **and** `go get github.com/STECH-Super-App/go-common@<the new sha>` in the same commit. `i18n-catalog`'s validator runs `ValidateBundleComplete` against its **pinned** go-common version, so a catalog PR that adds the en section without bumping the pin fails with "has no matching NotificationType" — a real incident (2026-07-13, `team_member_left`) that looked like unrelated CI breakage. Skipping the bump is soft at runtime (the compiled-in baseline still covers en; only non-en locales are affected until the catalog PR lands) but hard-red in CI.
3. Consumer services (notification-service, inbox-service) re-pin go-common and redeploy to pick up the new baseline entry and render the new type.
The `Missing` field on `LoadSummary` is the load-time backstop for this flow: a baseline key with no matching overlay `en` entry means step 2 was skipped or hasn't merged yet.

### `pkg/logger`
Structured logging using `uber-go/zap`.

- `New(level)` — returns a configured `*zap.Logger` for application code that owns a logger instance.
- Package-level `Warn(msg, fields...)`, `Info(msg, fields...)`, `Error(msg, fields...)` — for cross-cutting library code in go-common that emits diagnostics without holding a logger of its own. Backed by a lazily-initialized package logger driven by `LOG_LEVEL`.
- `String(key, value)`, `Int(key, value)` — field constructors re-exported from zap so callers don't need to import zap directly.

Application code: prefer `New(level)` for an injected logger. Library code in go-common: use the package-level helpers.

### `pkg/middleware`
Common HTTP middleware.
- `Logger`: Logs HTTP requests (status, latency, path, etc.).
- `CORS`: Handles Cross-Origin Resource Sharing.
- `AuthMiddleware`, `OptionalAuthMiddleware`, `RegistrationAuthMiddleware`, `AdminMiddleware`, `ClientMiddleware`: emit `COMMON_*` reasons via `pkg/errors` + `pkg/response` on rejection.
- `ParseUUIDParam(c, paramName, reason)`: validates an Echo path parameter as a UUID. On parse failure returns a 400 `*AppError` with the supplied reason and a message derived from `paramName`. Stops malformed UUIDs at the handler boundary so they never reach the repository layer (where Postgres would reject them with SQLSTATE 22P02 and leak as a 500).

### `pkg/metrics`
Prometheus metrics helpers.
- `NewCounter`, `NewGauge`, `NewHistogram`.
- Registers a default Prometheus registry.

### `pkg/tracing`
OpenTelemetry tracing setup.
- `Init`: Initializes the global tracer provider.
- `Start`: Starts a new trace span.

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

- **Header constants**: `HeaderEventID`, `HeaderEventType`, `HeaderAggregateType`, `HeaderAggregateID`, `HeaderOccurredAt`, `HeaderSchemaVersion`, `HeaderContentType`, `HeaderRetryCount`, plus legacy `HeaderOutboxID` for rolling-migration compat.
- **Value constants**: `ContentTypeProtoJSON`, `ContentTypeJSON`, `SchemaVersionV1`.
- **`Headers` type** (`map[string]string`) with typed accessors: `EventID()`, `EventType()`, `AggregateType()`, `AggregateID()`, `OccurredAt() (time.Time, error)`, `SchemaVersion()`, `ContentType()`, `RetryCount() int`.
- **`FromKafka([]kafka.Header) Headers`** — build typed view from raw Kafka headers.
- **Extractors**: `ExtractEventID` (reads `event_id`, falls back to legacy `outbox_id`), `ExtractOutboxID` (deprecated alias), `ExtractEventType`.

### `pkg/outbox`
Transactional Outbox Pattern for guaranteed at-least-once event delivery to Kafka, plus consumer-side idempotency.

- `New(pool, kafkaWriter, logger, cfg)`: Creates the full outbox subsystem.
- `Start(ctx)`: Launches background relay (poll → Kafka) and reaper (cleanup) goroutines.
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
| `OUTBOX_REAPER_INTERVAL` | `5m` | Cleanup schedule |
| `OUTBOX_RETENTION` | `72h` | Sent message retention before deletion |

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
ob := outbox.New(pool, kafkaWriter, zlog, outbox.DefaultConfig())
stop := ob.Start(ctx); defer stop()

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

- `NewDispatcher(reader, dlq, opts ...)` — DLQ is a **required** positional arg; no code path drops a failed message. Options: `WithRetry(w)`, `WithDedup(d)` (takes any value satisfying `Process(ctx, id, fn) error` — typically `*outbox.Deduplicator`), `WithMaxRetries(n)` (default 3), `WithLogger(l)`.
- `Handle[T proto.Message](d *Dispatcher, fn func(ctx, T) error)` — register a typed handler. Routing key is derived from `proto.MessageName(*new(T))`; protojson-unmarshal into a fresh `T` per message.
- `d.Run(ctx) error` — poll loop; returns `ctx.Err()` on cancel.
- `TopicName(eventsv1.Topic) string` — converts the proto `Topic` enum to its wire name (e.g. `TOPIC_USER_EVENTS` → `"user-events"`).
- `ErrPoisonPill` — sentinel for non-retryable handler errors (wrap with `%w`; goes straight to DLQ without consuming retry budget).

**Failure classification (→ DLQ with `x-dlq-reason`):**
- `ErrPoisonPill` → `poison_pill`
- `protojson.Unmarshal` failure → `unmarshal_error`
- Handler panic → `handler_panic` (panic is recovered and converted to an error)
- Any other error → `max_retries` after the retry budget is exhausted; forwarded to retry topic (if configured) otherwise

**Every DLQ message carries:** `x-dlq-reason`, `x-dlq-error` (truncated), `x-dlq-first-seen-at`, `x-dlq-last-seen-at`, plus the original envelope.

**Quick Start (consumer):**
```go
import (
    usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
    teamsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/teams/v1"
    "github.com/STECH-Super-App/go-common/pkg/events"
    "github.com/STECH-Super-App/go-common/pkg/outbox"
)

reader := kafka.NewReader(/*...*/)
dlqWriter := &kafka.Writer{Topic: "user-events-dlq", /*...*/}
dedup := outbox.NewDeduplicator(pool)

disp := events.NewDispatcher(reader, dlqWriter, events.WithDedup(dedup))

events.Handle(disp, func(ctx context.Context, e *usersv1.UserUpdated) error {
    return sessionService.RevokeAllForUser(ctx, e.UserId)
})
events.Handle(disp, func(ctx context.Context, e *teamsv1.TeamSuspended) error {
    return sessionService.RevokeAllForTeam(ctx, e.TeamId)
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
