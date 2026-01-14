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
