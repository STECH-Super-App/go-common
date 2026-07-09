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
Server-side translation wrapper around `nicksnyder/go-i18n/v2`. Loads JSON translation files from an `fs.FS`, resolves keys with locale-fallback semantics, and emits warn logs when a target-locale translation is missing but the default-locale one exists.

Translation files are named `<locale>.json` (e.g., `en.json`, `ru.json`). Locale matching uses `golang.org/x/text/language`: `ru-RU` resolves to `ru` if `ru` is loaded.

```go
import (
    "embed"
    "io/fs"

    "golang.org/x/text/language"
    commoni18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

//go:embed translations
var translationsFS embed.FS

func newBundle() (*commoni18n.Bundle, error) {
    sub, _ := fs.Sub(translationsFS, "translations")
    return commoni18n.LoadBundle(sub, language.English)
}

str, err := bundle.Resolve("ru", "tenant.transfer.expired_sms", map[string]any{
    "ExpiryTime": t.ExpiryTime.Format(time.RFC3339),
})
```

`Bundle.Resolve(locale, key, params)` returns the localized string. Missing key returns `ErrKeyNotFound`. Engine/template failure returns `ErrTranslationFailed`.

`SharedBundle()` exposes go-common's embedded shared `error.*` translations (en, ru, kk) — `error.unauthorized`, `error.internal`, `error.validation_failed`, `error.not_found`, `error.forbidden`. Services compose this with their own service bundle as a layered fallback.

### `pkg/notifyrender`
Renders in-app/push notification templates. `notifyrender.Renderer` is constructed by the consuming service from an `*i18n.Bundle` the service builds at startup from a mounted source (see the `i18n-catalog` repo). go-common carries the render *logic* (`typeKey`, `requiredParams`, `Render`, `ValidateBundle`) but **no strings** — the notification text lives in the external `i18n-catalog` data repo and is mounted into the service at runtime via a Kubernetes ConfigMap.

```go
import (
    "golang.org/x/text/language"
    commoni18n "github.com/STECH-Super-App/go-common/pkg/i18n"
    "github.com/STECH-Super-App/go-common/pkg/notifyrender"
)

// At service boot (main.go):
bundle, err := commoni18n.LoadBundle(os.DirFS(cfg.I18nBundleDir), language.English)
if err != nil {
    logger.Fatal("load i18n bundle", zap.Error(err))
}
renderer := notifyrender.NewRenderer(bundle)

// In the application layer:
title, body, err := renderer.Render(notificationType, params, locale)
```

Package-level helpers that stay unchanged: `ExtractParams`, `RequiredParams`, `IsVerbatim`, `RenderVerbatim`, and the `Reason*` error constants / constructors (`ErrUnknownType`, `ErrMissingParam`, `ErrEmptyPayload`, `ErrEmptyVerbatimText`).

`ValidateBundle(fs.FS) error` is the placeholder-consistency guard that `i18n-catalog` CI runs on every PR: it checks that every `{{.placeholder}}` in the source-locale `en.json` is declared in `requiredParams` for its type, and every declared `requiredParam` appears in at least one placeholder. A partial bundle (subset of catalog types) is accepted; use the full `en.json` in CI to catch missing sections.

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

- `New(pool, kafkaWriter, logger, cfg)`: Creates the full outbox subsystem (publisher + relay + reaper, with the dedup-table cleaner wired in automatically).
- `Start(ctx)`: Launches background relay (poll → Kafka) and reaper (cleanup) goroutines.
- `Migrate(postgresURL)`: Runs the embedded goose migrations (outbox + dedup tables).
- `RunInTx(ctx, pool, fn)`: Executes a function within a Postgres transaction.

**Relay claim lifecycle:** each poll claims its batch by atomically flipping rows `pending → processing` (single `UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED)` with `claimed_at` stamped), so concurrent relay replicas never forward the same row. Successful Kafka writes flip the batch to `sent`; failed writes are released straight back to `pending` (next poll retries, ~`OUTBOX_POLL_INTERVAL` latency). Claims orphaned by a crashed relay are recovered by the reaper's `ReleaseStuck` backstop after `ClaimTimeout` (`OUTBOX_CLAIM_TIMEOUT`, default 5m; zero-value in a hand-built `ReaperConfig` falls back to the default).

**Reaper:** one cycle per `Interval` runs three independent steps — delete `sent` rows older than `Retention`, release stuck `processing` claims older than `ClaimTimeout`, and (when a `Deduplicator` is attached via `NewReaper(..., WithDeduplicator(d))` — `outbox.New` does this automatically) delete `processed_outbox_messages` rows older than `Retention` so the dedup table doesn't grow unbounded.

**Publishing:**
- `Publisher.PublishProto(ctx, tx, opts)` — **preferred**. Typed proto message path. Serializes via `protojson` (snake_case), auto-injects the full envelope header set (`event_id`, `event_type`, `aggregate_type`, `aggregate_id`, `occurred_at`, `schema_version`, `content_type`). Generates `event_id` UUID and writes it to both the header and the proto payload's `event_id` field.
- `Publisher.Publish(ctx, tx, opts)` — legacy any-typed JSON path. Retained for transitional compat during the events-to-proto rollout; removed in the cleanup PR.

**Consumer-side idempotency:**
- `Deduplicator.Process(ctx, eventID, fn)` — claims the event_id FIRST via `INSERT ... ON CONFLICT DO NOTHING` inside a Postgres transaction, then runs `fn` in the same transaction only when the insert took the row. A conflict means already processed (or being processed by the claim holder): `fn` is skipped and nil returned. If `fn` errors, the transaction rolls back and the claim is released (safe to retry). The insert-first claim is what makes it atomic under concurrent first-time deliveries — exactly-once processing on top of at-least-once Kafka delivery. The reaper trims recorded event_ids older than `Retention` (`CleanupProcessed`).

**Environment Variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `OUTBOX_POLL_INTERVAL` | `1s` | Relay polling frequency |
| `OUTBOX_BATCH_SIZE` | `100` | Messages per poll cycle |
| `OUTBOX_REAPER_INTERVAL` | `5m` | Cleanup schedule |
| `OUTBOX_RETENTION` | `72h` | Sent message retention before deletion (also dedup-table retention) |
| `OUTBOX_CLAIM_TIMEOUT` | `5m` | Age after which a stuck `processing` claim is released back to `pending` |

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
