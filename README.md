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

### `pkg/outbox`
Transactional Outbox Pattern for guaranteed at-least-once event delivery to Kafka.

- `New(pool, kafkaWriter, logger, cfg)`: Creates the full outbox subsystem.
- `Start(ctx)`: Launches background relay (poll → Kafka) and reaper (cleanup) goroutines.
- `Publisher.Publish(ctx, tx, opts)`: Writes an event into the outbox table within a DB transaction.
- `RunInTx(ctx, pool, fn)`: Executes a function within a Postgres transaction.
- `DBTX`: Interface abstracting `pgx.Tx` and `*pgxpool.Pool` for repository compatibility.
- `Migrate(postgresURL)`: Runs the embedded goose migrations (outbox + dedup tables).
- `Deduplicator.Process(ctx, outboxID, fn)`: Consumer-side idempotency — skips `fn` if `outboxID` was already processed.
- `ExtractOutboxID(headers)`: Reads `outbox_id` from Kafka message headers.
- `ExtractEventType(headers)`: Reads `event_type` from Kafka message headers.

**Environment Variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `OUTBOX_POLL_INTERVAL` | `1s` | Relay polling frequency |
| `OUTBOX_BATCH_SIZE` | `100` | Messages per poll cycle |
| `OUTBOX_REAPER_INTERVAL` | `5m` | Cleanup schedule |
| `OUTBOX_RETENTION` | `72h` | Sent message retention before deletion |

**Quick Start:**
```go
// In main.go — after existing migrations
outbox.Migrate(cfg.Postgres.URL)

ob := outbox.New(pool, kafkaWriter, zlog, outbox.DefaultConfig())
stop := ob.Start(ctx)
defer stop()

// In a use case — atomic domain write + event publish
outbox.RunInTx(ctx, pool, func(tx pgx.Tx) error {
    if err := repo.WithTx(tx).Create(ctx, entity); err != nil {
        return err
    }
    return ob.Publisher.Publish(ctx, tx, outbox.PublishOptions{
        AggregateType: "user",
        AggregateID:   entity.ID,
        EventType:     "user.created",
        Topic:         "user-events",
        Payload:       event,
    })
})
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
