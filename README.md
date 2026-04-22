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
Standardized error handling types.
- `AppError`: Custom error struct with HTTP status code and JSON-friendly message.
- Helpers: `BadRequest`, `Unauthorized`, `Forbidden`, `NotFound`, `InternalServerError`.

### `pkg/logger`
Structured logging using `uber-go/zap`.
- `New(level)`: Returns a configured `*zap.Logger`.

### `pkg/middleware`
Common HTTP middleware.
- `Logger`: Logs HTTP requests (status, latency, path, etc.).
- `CORS`: Handles Cross-Origin Resource Sharing.

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
