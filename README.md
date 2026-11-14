# go-common

Shared library for STECH microservices.

> **Release 1 of the audit-fix campaign (14.11.2026) has ONE intentionally breaking point.** `outbox.New`, `outbox.NewRelay` and `events.NewDispatcher` (its DLQ writer and its `WithRetry` writer) now **panic at construction** when handed a `*kafka.Writer` that is `Async` or has `RequiredAcks == RequireNone` — kafka-go's zero value. Such a writer reports success before any broker answered, so the relay marked rows `sent` and the dispatcher committed offsets behind writes that never happened (SL-01). A service that still builds its writers by hand will **fail at start-up after the bump**; build them with `kafkawriter.NewProducer` / `kafkawriter.NewTopicWriter` ([`pkg/kafkawriter`](#pkgkafkawriter)). Everything else in the release is additive. The fleet checklist is `CHANGELOG.md` → "UPGRADING — release 1".

> **Release 2 (audit-fix campaign wave 4) adds two start-up panics.** `events.NewDispatcher` needs a consumer group (the reader's `GroupID`, or `events.WithGroup`) — the old default `"unknown"` made two consumers share dedup claims. `outbox.New` / `outbox.NewRelay` need a writer that partitions by key (`kafkawriter.NewProducer` does; a hand-built one needs `Balancer: &kafka.Hash{}`). The REST idempotency table is `common_idempotency_keys`, never `idempotency_keys` (three services own that name). Steps: `CHANGELOG.md` → "UPGRADING — release 2".

## Packages

### `pkg/auth` — claims, identity headers, acting-tenant selector

`Claims` is the access-token payload. **There is no `chat_read` claim any more (SL-25):** it grew with a person's relationships and broke web login past the 4,096-byte cookie limit (Critical Rule 20). `ChatReadContext`, its codec, `HeaderChatReadContext`, the 64 KiB header constants and `ValidateChatAccessTokenSize` are gone. A token minted earlier may still carry the key; `encoding/json` ignores it, so those tokens keep decoding (pinned by `TestClaimsIgnoreRetiredChatReadClaim`) — never give the decoder `DisallowUnknownFields`. Messenger authorises from `tns` / `tns_ro` through the `pkg/authz` headers.

`HeaderActingTenantID` (`X-Acting-Tenant-Id`) and `ParseActingTenantID` stay: a client selector (which of my seats am I acting as), never proof of membership. `Header*` constants name the identity headers api-gateway writes; the full strip list is in [`pkg/trustedheaders`](#pkgtrustedheaders). `HeaderRealIP` / `HeaderForwardedFor` are retired names kept only so the strip list and old code compile: **nothing may read them** (`.golangci.strict.yml` bans it) — use [`pkg/clientip`](#pkgclientip).

### `pkg/clientip`

The one way a service learns the caller's address (audit-fix spec §3.3, GW-04, GW-07, ID-24). api-gateway is the single authority: it takes the address from the ingress only, deletes every incoming address header and writes one of its own, `X-Client-IP`. Services read only that.

```go
addr, ok := clientip.FromEcho(c)   // netip.Addr, validated; ok == false = absent
if !ok { /* the CALLER decides: refuse a login, count a view as unknown */ }
```

- `Parse`, `FromHeader`, `FromRequest`, `FromEcho` read **`X-Client-IP` only** — never `X-Forwarded-For`, `X-Real-IP`, `Forwarded`, and **never the TCP peer as a fallback** (behind ingress the peer is the ingress pod; a silent fallback is how every rate limit would share one bucket). A value is accepted when it is a bare IP of at most `MaxLen` (45) bytes, no zone, not unspecified or multicast, and the header occurs **once**; the result is canonical (IPv4-mapped IPv6 unmapped, IPv6 lower-case).
- `NewContext` / `FromContext` carry it on a `context.Context`; `Middleware()` puts the request's address there (`httpserver.New` installs it).
- gRPC: `UnaryClientInterceptor` / `StreamClientInterceptor` write the context's address into metadata `x-client-ip` (and remove a hand-written one); `UnaryServerInterceptor` / `StreamServerInterceptor` read it back, validated, into the called service's context. Attach them beside `requestid`'s pair, with `grpc.WithChainUnaryInterceptor` / `grpc.ChainUnaryInterceptor`. A service that calls another **on a user's behalf** and wants it to see the user's address needs the client interceptor and a context that came through `Middleware`.
- `Set(header, addr)` **is reserved for api-gateway**: it replaces whatever was there and, for an unusable address, leaves no header. `.golangci.strict.yml` bans calling it elsewhere (the gateway's one call carries `//nolint:forbidigo // <reason>`).
- `middleware.ClientMiddleware` now takes the address from here: a request without a valid `X-Client-IP` is `400 COMMON_CLIENT_INFO_INVALID`. **Roll api-gateway out first.**

### `pkg/trustedheaders`

The one list of request headers a client must never be able to set on a request that reaches a service: `GatewayAsserted()` (the identity headers from `pkg/auth` / `pkg/authz`), `ClientAddress()` (`X-Real-IP`, `X-Client-IP`, `Forwarded`, `X-Forwarded-For/-Port/-Prefix/-Host/-Proto`, `X-Original-Forwarded-For` — what ingress-nginx renames a client's own `X-Forwarded-For` to when it does not trust it), `TraceContext()` (`traceparent`, `tracestate`, `baggage`), `Retired()` (`X-Chat-Read-Context`, `X-Team-Memberships`), their union `StripAtEdge()`, and `Strip(http.Header)` (case-insensitive). All names are canonical and every call returns a fresh slice.

The gateway calls `Strip` on every inbound request before it writes its own headers. Its test sets every name in `StripAtEdge()` and asserts none survives, and asserts every header it sets is in the list. This package's own test reads `pkg/auth` and `pkg/authz` and fails when a new `Header*` constant is in neither the list nor its client-supplied exemptions (`X-Device-Id`, `User-Agent`, `X-Acting-Tenant-Id`), so a header a service trusts cannot be added without being stripped.

### `pkg/authz` — tenant membership headers

Three headers carry a caller's tenant seats; the gateway sets all of them from verified token claims (`auth.Claims`) and strips client copies. Never split the strings by hand.

| Header | Format | Source claim | Readers |
|---|---|---|---|
| `X-Tenant-Memberships` | `<tenant>=ADMIN,…` | `tns` (active seats only) | every service — `ParseTenantMemberships` / `FormatTenantMemberships` |
| `X-Tenant-Membership-Since` | `<tenant>=<unix>,…` | `tns[].at` | chat-service only — `FormatMembershipSince` / `ParseMembershipSince` |
| `X-Tenant-Readonly-Memberships` | `<tenant>=<ROLE>=<unix>,…` | `tns_ro` (seats in SUSPENDED tenants) | chat-service only — `FormatReadonlyMemberships` / `ParseReadonlyMemberships` |

`X-Tenant-Memberships` is never changed to carry a start time or a suspended seat: sale's PHP parser drops, and `ParseTenantMemberships` would read into the role, any entry with extra text, and every other service authorizes writes from it assuming each entry is active. That is why the extra data has its own headers. A golden-string test pins the header's bytes for claims that carry `at`.

- **Strict parsing.** The two chat-only parsers reject the whole header with a typed 400 (`COMMON_TENANT_MEMBERSHIP_SINCE_INVALID`, `COMMON_TENANT_READONLY_MEMBERSHIPS_INVALID`) on: a tenant id that is not a UUID (`pkg/input`) or is the nil UUID, an unknown role (long form `ADMIN`/`MANAGER`/`OPERATOR` only), a negative, non-decimal or post-9999 Unix time, a repeated tenant, an empty entry, a wrong field count, or more than `MaxMembershipHeaderEntries` (64) entries. An empty header gives an empty result and no error. Tenant ids come back lower-case; times are UTC.
- **Fail-closed formatting.** The formatters omit any claim the parser would refuse (bad id, unknown role, out-of-range time, repeated id — first wins) and any entry without a start time (`at == 0` is "no baseline", never a zero baseline), so `Parse(Format(x))` never errors and one bad seat costs only itself.
- **One role expansion.** `ExpandTenantRole` turns the token's compact role (`A`/`M`/`O`) into the long form; unknown codes pass through verbatim. The gateway uses it for all three headers.
- **The bound.** `tns` plus `tns_ro` together hold at most 16 entries (tenant-service's membership cap, enforced where tokens are minted); the 64-entry parser limit is only a transport sanity bound.
- **`ParseTenantMembershipsStrict(header) ([]TenantMembership, error)`** is the same strictness for `X-Tenant-Memberships` itself (SL-29): every entry `<uuid>=<role>`, tenant id lower-cased, nil UUID / repeated tenant / empty entry / more than 64 entries / a role with a separator, whitespace or control character → typed 400 `COMMON_TENANT_MEMBERSHIPS_INVALID`, whole header, no partial result. `ParseTenantMemberships` stays lenient for compatibility (it accepts non-UUID ids and reads past a malformed entry — the fleet's unit tests use such ids); new code should use the strict one. `FormatTenantMemberships` now also omits an entry whose id or role is empty or holds `,` or `=` (a role `ADMIN,<id>=ADMIN` would have forged a second membership), and `FormatRolesCSV(roles)` does the same for `X-User-Roles`.

### `pkg/config`
Helper functions for loading configuration from environment variables.
- `GetEnv(key, default)` — an **unset or empty** variable counts as absent: the default is returned. (It used to return `""` for a set-but-empty variable, so a ConfigMap key rendered empty replaced a default such as the broker list; use `os.LookupEnv` where an explicitly empty value must mean something.)
- `GetEnvInt/Int8…Int64/Uint…Uint64/Float/Float32/Bool/Duration(key, default)` — absent or empty → default silently; **set but unparseable** (`SOME_TIMEOUT=30` without a unit, `X_ENABLED=yes`) → default, and the `InvalidHandler` is told the key and the wanted type (never the value: it may be a secret). The default handler writes one JSON warn line to stderr; configuration is read before the logger exists, which is why this is a hook. `SetInvalidHandler(h)` replaces it and returns the previous one.

### `pkg/db`
Database connection factories.
- **Postgres**: `NewPostgres` creates a `*pgxpool.Pool` (using `jackc/pgx/v5`) and pings it. A **zero `PostgresConfig` field means "not set"**: the value parsed from the URL (`pool_max_conns=…`) or pgx's own default stays, and only a non-zero field overrides it — an unset `MaxConns` used to fail pool creation and an unset `MaxConnLifetime` used to expire every connection on release. A pool that fails its ping is closed.
- **Redis**: `NewRedis` creates a `*redis.Client` and pings it (closed on failure). `RedisConfig` also takes `TLS` (TLS 1.2+, system roots) and `DialTimeout` / `ReadTimeout` / `WriteTimeout` (zero keeps go-redis's defaults).
- Failures are typed 500s (Critical Rule 7): `COMMON_DB_CONFIG_INVALID`, `COMMON_DB_UNAVAILABLE`, driver error as the cause.

### `pkg/egress`
The outbound HTTP client for every call to the public internet (spec dev-setup `docs/superpowers/specs/2026-11-04-egress-service-design.md` §4.4). Build it in `main` and pass it to each provider client; never use `http.DefaultClient` for an external host.
- `HTTPClient(timeout)` / `Transport()` read `EGRESS_PROXY_URL` (the caller's own egress-proxy port, `http://host:port` only); `NewTransport(Config)` takes the values explicitly. The transport is built from a literal carrying `http.DefaultTransport`'s settings (`ForceAttemptHTTP2`, dial/idle/TLS timeouts) with its `Proxy` fixed to that URL — it no longer clones `http.DefaultTransport` through a type assertion, which panicked at start-up as soon as any imported package wrapped the default transport (SL-32). `HTTPClient` refuses a non-positive timeout (`COMMON_EGRESS_TIMEOUT_INVALID`: a zero `http.Client.Timeout` waits for ever) and clients built from the same configuration **share one transport** and its connection pool; `NewTransport` / `Transport` still return a fresh transport each call.
- `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY` are never read: a process-wide proxy would route internal gRPC and tracing into egress.
- An empty URL is a typed error `COMMON_EGRESS_NOT_CONFIGURED`, never a silent direct call. The one opt-out is `EGRESS_DIRECT=true` together with `APP_ENV=local` (a binary run on the host, outside compose). A malformed URL is `COMMON_EGRESS_PROXY_URL_INVALID`.

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
func ParsePage(rawNumber, rawSize string, defaultSize, maxSize int32) (Page, *Failure)  // page in [1, DefaultMaxPage = 10000]
func ParsePageMax(rawNumber, rawSize string, defaultSize, maxSize, maxPage int32) (Page, *Failure) // the same with the deepest page stated by the caller
func MaxItems(n, maxItems int) *Failure
func Coordinates(lat, lon float64) (float64, float64, *Failure)           // finite, lat [-90,90], lon [-180,180], never (0,0); -0 out as 0
func Phone(raw string) (string, *Failure)                                 // '+' required, no letters, libphonenumber IsValidNumber, E.164 out
func Email(raw string, maxRunes int) (string, *Failure)                   // rule «B»: ASCII dot-atom local part ≤ 64; domain ASCII or Cyrillic (IDNA, single-script labels), stored in A-label form ≤ 253, whole ≤ 254; no DNS
func EmailDisplay(stored string) string                                   // stored address → the domain's Cyrillic labels in Unicode; display only
func Collect(fields ...*errors.FieldError) []errors.FieldError            // drops nils; nil when nothing failed
func GRPC(fields ...errors.FieldError) error                              // InvalidArgument + errdetails.BadRequest; nil for no fields
func MessageFor(reason string) string                                     // English fallback for AppError.Message
```

`Line`/`Body` processing order: (1) invalid UTF-8 → `COMMON_TEXT_INVALID_CHARACTERS`; (2) trim leading/trailing whitespace, `Cc` and `Cf`; (3) character rule — any `Cc` (Body exempts TAB/LF/CR), and in `Line` also U+200E/F, U+202A–E, U+2066–9, U+2028/9 → `COMMON_TEXT_INVALID_CHARACTERS`. The rule is a copy of sale-service `app/Support/Validation/ControlCharacters.php`, pinned by a port of its unit vectors and an exhaustive all-code-points test. Interior `Cf` (ZWJ, ZWSP, BOM) is allowed — emoji sequences need ZWJ; (4) blank (only whitespace / `Cf` / variation selectors / default-ignorables) → `COMMON_TEXT_REQUIRED` with `Required()`, else `""`; (5) rune count > max → `COMMON_TEXT_TOO_LONG {max}`. No Unicode normalisation.

**E-mail, rule «B» (owner decision 14.11.2026, SL-19).** The local part is ASCII only; the domain may be ASCII or a **Cyrillic** internationalised domain; every label is single-script.

| address | verdict |
|---|---|
| `ivan@mail.ru` | accepted |
| `ivan@почта.рф` | accepted — stored `ivan@xn--80a1acny.xn--p1ai` |
| `ivan@xn--80a1acny.xn--p1ai` (any case) | accepted — **the same stored value** |
| `ivan@почта.ru` / `ivan@mail.рф` | accepted: each label is single-script, and the DNS allows it |
| `иван@почта.рф`, `ivаn@mail.ru` (Cyrillic «а») | refused: local part is ASCII only |
| `ivan@mаil.ru`, `ivan@почtа.рф` | refused: one label mixes Latin and Cyrillic |
| Greek, Armenian, Han, accented-Latin domains and their `xn--` forms | refused: Cyrillic is the only non-ASCII script |
| `xn--` garbage, leading/trailing hyphen, empty label, trailing dot, no dot, A-label over 63 octets | refused |
| zero-width, bidi, soft-hyphen characters anywhere | refused |

Mechanism: trim, refuse whitespace/control/format characters, NFC-normalise; each label is checked against the single-script rule **before** it is case-folded (so «İ» cannot become an ASCII «i»), then validated and encoded by `golang.org/x/net/idna` (Registration profile). The A-label form is **the one stored form**: always ASCII, so two spellings of one mailbox collide in a unique index or a `WHERE email = $1`, and SMTP libraries and providers take it unchanged. The caps (63 per label, 253, 254, `maxRunes`) are measured on that stored form. The local part keeps its case (release 1). `EmailDisplay(stored)` returns the form to show a person (`ivan@почта.рф`); only Cyrillic labels are decoded — a domain with any other `xn--` label comes back as stored.

**Services:** any place that compares, looks up or uniquely indexes an e-mail must pass the typed value through `input.Email` first, and use only its result as the key. Never lower-case, trim or compare a raw address yourself, and never send mail to `EmailDisplay`'s output.

**Hardening of 14.11.2026 (SL-17…SL-20).** (1) Step 2 trims whitespace, `Cc` and a *named* list of invisible format runes (BOM, ZWSP/ZWNJ/ZWJ, direction marks, bidi controls, word joiner and invisible operators, soft hyphen, ALM) — not the whole `Cf` category, which holds the emoji tag characters that end a flag sequence (U+E0020–E007F) and the Arabic number signs that start a number (U+0600–0605, U+06DD): a text ending in the flag of Scotland is now stored as typed. (2) `Body` refuses **unbalanced** bidi embedding/override/isolate controls (LRE, RLE, LRO, RLO need a PDF; LRI, RLI, FSI a PDI): an unclosed RLO reverses everything after it, which let a description show a phone number or link backwards; balanced pairs and LRM/RLM stay allowed. This is the one place `Body` departs from sale-service `ControlCharacters::found`, which the PHP side must follow. (3) A required field of U+2800 (braille blank) is now blank, with the Hangul/Khmer fillers. (4) `Email`: the local part is an RFC 5322 dot-atom in ASCII (letters, digits, `!#$%&'*+-/=?^_`{|}~`, single dots between atoms) — a comma (a second SMTP recipient), angle brackets, quotes and every non-ASCII letter (a Cyrillic «а» posing as a Latin one) are refused; length limits 64 / 63 per label / 253 / 254; a numeric last label (`a@127.0.0.1`) is refused; the domain rule of release 2 is the e-mail paragraph above. **Case is kept**: `User@x.ru` and `user@x.ru` are two values; compare lower-cased where identity matters. (5) `ParsePage` refuses a page beyond `DefaultMaxPage` (10 000): a page is an offset Postgres walks, and `?page=2000000000` cost a full scan per request. A list that must go deeper calls `ParsePageMax` with its own bound.

`*Failure` (`Reason`, `Params`) implements `error`, so a domain constructor can return it and a handler finds it with `errors.As`. The library is **status-free**: the service picks the HTTP status. Field names are attached by whoever knows them:

```go
name, nameF := input.Line(req.Name, 120, input.Required())
id, idF := input.UUID(c.Param("id"))
if details := input.Collect(nameF.At("name"), idF.At("id")); details != nil {
    return response.JSONError(c, commonErrors.New(http.StatusBadRequest).
        Reason(details[0].Reason).Message(details[0].Message).Details(details).Build())
}
```

`At` is nil-safe (a nil `*Failure` yields nil), which is what makes the `Collect` idiom work. A `ParsePage` failure names the input that failed through `AtPage(numberField, sizeField)`. `Phone` additionally refuses an interior control/format character and an extension (E.164 cannot carry one). `Coordinates` judges the pair, so the caller attaches the point's one field name (`.At("location")`); its bounds are the fixed WGS 84 range, not a per-call choice. Exactly (0, 0), either sign of zero, is `COMMON_COORDINATES_UNSET` rather than invalid: it is what a client sends when it has no point, and no address in this market lies at null island, so an app can ask for a point on the map. A single zero axis (the equator, the prime meridian) stays valid. Stricter domain rules (organisation's +7, VIN grammar) stay in the service, applied to the cleaned value.

Reasons (exported as `Reason*` constants): `COMMON_TEXT_REQUIRED`, `COMMON_TEXT_TOO_LONG {max}`, `COMMON_TEXT_INVALID_CHARACTERS`, `COMMON_UUID_INVALID`, `COMMON_NUMBER_INVALID`, `COMMON_NUMBER_OUT_OF_RANGE {min,max}`, `COMMON_TOO_MANY_ITEMS {max}`, `COMMON_PHONE_INVALID`, `COMMON_EMAIL_INVALID`, `COMMON_COORDINATES_INVALID` (NaN, ±Inf or out of range), `COMMON_COORDINATES_UNSET` (exactly (0, 0)), `COMMON_INPUT_REJECTED_BY_STORAGE` (the `pgerr` backstop), `COMMON_REQUEST_ENCODING_INVALID` (api-gateway).

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

`InvalidInput(err) (*input.Failure, bool)` recognises `*pgconn.PgError` SQLSTATE `22021`, `22P05`, `22001`, `22P02` and — since 14.11.2026 (SL-21) — `22003` (numeric overflow: 1e15 into `numeric(12,2)`), `22007`, `22008` (bad / out-of-range date) and `54000` (value too large for an index row), plus pgx v5's client-side integer out-of-range encode error (no exported type; matched on its message, pinned by a test that produces the real error through `pgx.ExtendedQueryBuilder`). `22003` can also come from server-side arithmetic on valid inputs; it is counted anyway, because a 4xx with an alert beats a Reason-less 500. A hit returns a `COMMON_INPUT_REJECTED_BY_STORAGE` `*input.Failure` and moves `stech_input_backstop_total{sqlstate}`; every series is pre-created at 0.

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
- `response.JSONError(c, err)` — 4xx/5xx with `{success: false, error: {code, reason, message, params, details}}`. It renders: an `*AppError` (all of `Reason`, `Params`, `Details` forwarded; a `Code` outside 100..599 — a zero value used to panic `WriteHeader` — is sent as 500); an `*input.Failure` (422 with its Reason and Params, or 400 for the storage backstop `COMMON_INPUT_REJECTED_BY_STORAGE`, whose cause never reaches the client); an `*http.MaxBytesError` (413 `COMMON_REQUEST_TOO_LARGE`, even when `c.Bind` wrapped it in a 400); an `*echo.HTTPError` — a failed `c.Bind`, the router's 404/405 — with its status, a `COMMON_*` Reason by status (`COMMON_BAD_REQUEST`, `COMMON_NOT_FOUND`, `COMMON_METHOD_NOT_ALLOWED`, `COMMON_REQUEST_TOO_LARGE`, `COMMON_UNSUPPORTED_MEDIA_TYPE`, `COMMON_TOO_MANY_REQUESTS`, `COMMON_INTERNAL` for 5xx) and the status text, never the bind error's parser internals; anything else is a 500 with Reason `COMMON_INTERNAL`, so even an unclassified fault has an i18n key (SL-14).

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
- **Fail-soft per-key validation, not fail-fast.** `WithAllowedParams(func(key string) ([]string, bool))` supplies a placeholder allow-list. Every overlay value is **parsed as the template it will be rendered as** at load: one that does not parse (`{{.name}`), or whose parse tree reads a name outside the allowed set, is dropped (falls back to baseline) and listed in `LoadSummary.Rejected` as `"<locale>/<key>"`. The names come from walking the parse tree (`i18n.Placeholders(s) ([]string, error)`), not a pattern, so `{{if .x}}`, `{{- .x -}}`, `{{ .x | printf "%s" }}`, `{{else if .x}}`, `{{.x.y}}` and `{{index . "x"}}` are all seen (SL-07). Parsed templates are cached in the `Snapshot`, so a render is an `Execute`, not a `Parse`.
- **A render error falls through.** `Resolve` tries matched-locale overlay → en overlay → baseline and takes the first layer that **renders**; a value that parses but fails at run time (an incompatible comparison) is logged at Warn and skipped instead of failing every message of its type. Only when every layer fails does `Resolve` return the error (`ErrTranslationFailed`).
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

**Two param tiers.** `requiredParams` is the producer-side contract: `notifyoutbox` rejects a directive whose required param is empty (there, empty means missing), and `Render` returns `ErrMissingParam` if the key is absent. `optionalParams` is the weaker tier for fields that may legitimately arrive empty. It began as `request_no`, the human-facing delivery request number (Д-13), which rides on 9 of the 18 `SendDelivery*` payloads but is empty on directives emitted before numbering shipped; since the parts catalog landed it also covers **nine parts types** — `product_name` (an unmatched позиция has no card to take a name from, so the producer sends `""` rather than echoing the артикул), `price` / `price_from` (a позиция with no ladder rung has no price), `remaining_causes` (Р56·В-54's conditional lift text, where an EMPTY list is the *good* case) and `new_address_count` (Р31's second-edition tail). The repair family widens the tier once more: `request_no` on all 23 deal types (order-service stamps the human-facing number, and a directive may precede it), `machinery` and `price` where the sentence survives without them, `provider_name` on the expiry text (each `had_offer` arm names the responder only if there is one to name), `distance_km` through `countOrEmpty`, `reason` on `POSTING_CANCELLED` (the customer's cancel-reason label, printed as given behind `{{if .reason}}` — optional because the sale-service producer did not fill it when the field was minted), `posting_no` on the four **customer-addressed** board types (`RESPONSE_RECEIVED`, `RESPONSE_WITHDRAWN`, `POSTING_EXPIRING`, `POSTING_EXPIRED_CUSTOMER` — «Заявка #N», the customer's own posting number; the four responder-addressed board rows carry none by §5 R7, and it is **not** `request_no`, which is order-service's deal sequence), and the four **branch discriminators** `had_offer` / `deal_completed` / `cancelled_by` / `supersedes_previous`, which are optional for a structural reason worth stating: they are never printed, they pick between editions of a sentence, and a REQUIRED param must appear as a literal `{{.name}}` — so a discriminator can only live in the weaker tier. Templates read them as `{{if eq .x "…"}}` on **both** arms, never a bare `{{else}}` carrying the opposite edition, so an absent or unrecognised value lights neither branch and the reader is told less rather than something false (`repair_request_cancelled`'s third `{{else}}` arm prints the actor with no role noun for exactly that reason). Two non-repair types joined this sub-tier: `ORDER_CANCELLED` reads `cancelled_by` (`customer` → «by the renter», `provider` → «by the owner»), `decline_reason` (the renter's counter-offer decline, `PRICE` | `DATES` | `TERMS` | `FOUND_ANOTHER` | `OTHER`, one clause each; empty on every other cancel) and `cancel_reason` (a human rent cancel, one code of the renter's list `CHANGED_PLANS` | `FOUND_ANOTHER` | `WRONG_DETAILS` | `OTHER` or the lessor's `MACHINE_UNAVAILABLE` | `MACHINE_FAULTY` | `TERMS_NOT_SUITABLE` | `CUSTOMER_UNREACHABLE` | `OTHER`, one «Reason: …» clause each; empty on a decline and on every system cancel — the two reasons are never both set, and they are separate params because `FOUND_ANOTHER` and `OTHER` mean different sentences in the two lists) with explicit arms only, so an unknown token drops the agent or the clause; `DELIVERY_REQUEST_CANCELLED` reads `cancelled_by` (`customer` | `carrier`) to pick the subject noun and — because the token governs the WHOLE sentence — keeps a neutral default arm that names no side («Request #N was cancelled.»), never the raw token; since TESTIROVANIE-254 it also reads `cancel_reason`, the cancelling side's Д-18 code (customer `CHANGED_PLANS` | `FOUND_ANOTHER_CARRIER` | `WRONG_DETAILS` | `OTHER`, carrier `MACHINE_UNAVAILABLE` | `MACHINE_FAULTY` | `CUSTOMER_UNREACHABLE` | `OTHER`, a late cancel by either side `DELIVERY_PROBLEM`), one «Reason: …» clause each with explicit arms only. `DELIVERY_REQUEST_REJECTED` reads `reject_reason` the same way (the carrier's `NOT_ON_ROUTE` | `MACHINE_NOT_SUITABLE` | `NO_FREE_MACHINE` | `OTHER`). Both codes are optional, and an empty one renders the sentence that shipped before them. `supersedes_previous` is the single-armed exception and is allowed to be: its false edition is «say nothing», which is true under every value the wire can carry. `deal_completed` on `REPAIR_REVIEW_WINDOW_ENDING` is the **second** exception and is written `{{if eq .deal_completed "false"}}…{{else}}…{{end}}` deliberately (repair critique NOTIF-04): there the discriminator governs the WHOLE sentence rather than adding a suffix, so two explicit arms with no default would render an **empty body** for an absent value. Only the `"false"` arm asserts anything, and the `{{else}}` is byte-identical to the text that shipped before the field existed — the rule's purpose (never assert what the wire did not say) is kept; only its shape is not. The same field on `REPAIR_REVIEW_RECEIVED` keeps the ordinary two-armed form, because there it only appends a clause. The carrier listing pair's `interrupted_status` / `restored_status` (173/174, below) is the third exception, for the same reason. Rules for an optional param:

- The baseline **must** guard it: `{{if .request_no}} #{{.request_no}}{{end}}`, so an empty value renders the plain sentence instead of a dangling `#`. `TestBaselineGuardsOptionalParams` enforces the guard; `TestRenderDelivery_RequestNumber` pins the exact rendered string in all three states (populated / empty / key absent).
- `Render` fills an **absent** optional key with `""` before templating — the resolver runs `missingkey=error`, which trips on a missing key even inside an `{{if}}`, so without the fill a legacy caller would get a 500 instead of the fallback text.
- Never promote one to `requiredParams` to "make it render": that makes every not-yet-numbered directive fail to publish.
- A numeric optional param must be formatted to `""` and not `"0"` — Go's template truth test runs on the STRING, so `"0"` is non-empty and its `{{if}}` guard is always true. `countOrEmpty` does that; a **required** count must use `strconv.Itoa` instead, because there empty means missing.

**The shop's display name on twenty parts texts** (owner ruling of 07.09.2026, «Уведомления запчастей» :75). `shop_display_name` is an OPTIONAL param on exactly the twenty shop-subject parts types — 69, 70, 71, 72, 73, 74, 75, 78, 79, 80, 104, 117, 118, 120, 121, 126, 127, 128, 130 and 172 — read from a new proto field of that name, which the PRODUCER fills with the company's current display name (М-01) when it builds the directive, outside its write transaction, and never stores. Every template reads it as `{{if .shop_display_name}}…{{else}}<today's text>{{end}}`, so a failed directory lookup sends `""` and renders the nameless wording byte for byte (`TestShopNamedTextsKeepTodaysWordingWithoutAName`; 72 alone changed its fallback, to 71's sentence per the vault :138). It is never required: a required name would turn a tenant-service outage into a rolled-back order transition or a dead-lettered sale notice. `shop_name` stays reserved (B-5). 79 («Санкция снята с магазина») also gains two tail flags derived here from `remaining_causes`, exactly like `remaining_price_stale`: `remaining_only_paused` («…once you resume sales») and `remaining_only_verification_revoked` («…once your organisation is unblocked»), each `"1"` only when that cause is the ONLY one left (`causeOnly`), with price age keeping precedence. The causes are compared as `parts.storefront.v1.UnavailableCause` arm names (`SHOP_PAUSED`, `PRICE_STALE`, `SHOP_ADMIN_HIDDEN`, `SHOP_VERIFICATION_REVOKED`), which is how sale-service's `LiftDirectiveCauseMapper` spells them from the tracker wave's fix of 15.10.2026 on (earlier builds wrote lower-case values that light no flag). ⚠ Consumers (notification-service, inbox-service) must RUN this release before any i18n-catalog overlay that reads the new params is applied: `pkg/i18n` rejects an overlay value that references an undeclared placeholder, per value.

**The review-moderation family** (166–171, review-service): `ORDER_` (rent), `DELIVERY_` and `REPAIR_REVIEW_HIDDEN` carry the moderator's `reason` (required); the three `*_REVIEW_COMPLAINT_RESOLVED` carry В-66's `outcome` as the paired flags `outcome_hidden` / `outcome_no_violation`, with parts' 131 text byte for byte. All six are person-addressed — the hidden notice to the review's author, the resolved notice to the complaint's filer. For 170/171 that is an exception to repair's D28 team addressing, which still governs repair's non-deal notices: rule N of 08.10.2026 covers deal notices only (plus, since 09.10.2026, repair's open-posting notices), and a review-moderation notice is neither. Rule N is not built for repair yet, so for now 170/171 are the only `REPAIR_*` types that are person-addressed. ⚠ notification-service and inbox-service must run a go-common pin that carries this family — and 172 — BEFORE review-service (or any producer) emits one: an older pin cannot render the type and dead-letters it.

**The carrier listing's В-41(в) pair** (173 `DELIVERY_LISTING_BLOCKED`, 174 `DELIVERY_LISTING_RESTORED`; owner decision of 23.10.2026, OTDELRAZRABOTKI-731). sale-service's Delivery module writes them, not order-service, whose delivery types are the request family 47–64. They are written from its `organisation.events` fold when a company block hides the carrier's one listing and when the unblock returns it, tenant-addressed to the carrier's Владелец and Менеджеры (`[ADMIN, MANAGER]`, `[IN_APP, PUSH]`, deep link `MY_LISTINGS {listing_id}`). Each carries ONE optional param, the status the block interrupted (`interrupted_status` / `restored_status`, in delivery's lower-case `moderation` | `active` | `rejected` | `paused`). It is a discriminator, never printed and never required. Only `"active"` renders the vault's catalogue sentence; any other value, or an empty or absent one, renders a neutral edition that claims no catalogue change. The template is `{{if eq .x "active"}}…{{else}}…{{end}}`, like `REPAIR_REVIEW_WINDOW_ENDING`, because the token governs the whole sentence. `TestEveryDeliveryTypeIsCatalogued` is the delivery family's enum census (22 types). ⚠ notification-service (which reserves both: «не отключается») and inbox-service must run a pin that carries the pair BEFORE sale-service emits either.

**The listing-moderation notices name the vertical** (`LISTING_APPROVED`, `LISTING_REJECTED`, `LISTING_UNPUBLISHED`; OTDELRAZRABOTKI-958, owner's pick (b) of 10.11.2026). Each declares `listing_service` as its one optional param, beside the unchanged required `listing_title` (and `reason` on the two refusals). The vocabulary is `sale` | `rent` | `delivery` | `repair`, lower-case, the spelling of `SendChatSectionClosed.object_service`. It is a discriminator, never printed: the en baselines read it in explicit `{{if eq .listing_service "…"}}` arms with no `{{else}}` («Your sale / rental / delivery / repair listing '…' is now live.»), so an empty, absent or unknown value renders the text that shipped before the field, byte for byte, and the titles do not change (`listing_service_test.go`). It must stay optional: a producer on an older go-common sends no such field, and a required param would make `notifyoutbox` roll back that producer's moderation write. ⚠ The shipped i18n-catalog `notifyrender/en.json` shadows `BaselineEN`, so the new en wording reaches users only once that overlay carries it, and an overlay that reads `listing_service` may be applied only after notification-service and inbox-service run this release.

**Every person-ended deal names its reason** (owner decision of 16.10.2026, TESTIROVANIE-254). `ending_reason_test.go` is the catalog half of the cross-module guarantee (i18n-catalog's checker and the e2e suite are the other two). `TestEveryEndingNotificationIsClassified` walks `NotificationType_name` and takes every value whose name has an ending segment — `CANCELLED`, `CANCELED`, `REJECTED`, `DECLINED`, `WITHDRAWN`, `CLOSED`, `EXPIRED`, `TERMINATED`, `ABORTED`, `REFUSED`, `DENIED` — and fails unless it is classified exactly once: in `personEndedTypes` (a person ends the deal or request, and the type must name the reason) or in `endingTypesWithoutAReason` with one of four kinds and a why (a no-reason door or a type that IS its reason, a system or timer ending, a deal that continues, or not a deal at all). A new vertical's cancel type is therefore red the day `gen-go-lib` carries it. `TestPersonEndedNotificationsNameTheirReason` holds the eleven covered types — rent `ORDER_CANCELLED` (`cancel_reason`, `decline_reason`), `DELIVERY_REQUEST_REJECTED` (`reject_reason`), `DELIVERY_REQUEST_CANCELLED` (`cancel_reason`), `REPAIR_REQUEST_REJECTED`, `REPAIR_REQUEST_CANCELLED`, `REPAIR_POSTING_CANCELLED` and the five parts cancels/rejects (`reason`) — to the decision end to end: the payload field of the same name reaches the param through `ExtractParams`, the catalog declares it, and the English baseline puts it into the sentence. A LABEL must be a literal `{{.param}}` and appear in the render; a CODE must give every code of its closed vocabulary its own `{{if eq}}` arm, distinct wording and no raw token, and its chain may not end in a bare `{{else}}`, which would speak for an empty or unknown code too.

`ExtractParams` is a hand-written type switch over the envelope's payload oneof, so a new payload **field** with no line added to its arm would simply never reach a template. `extract_completeness_test.go` is the guard: it walks the oneof through protoreflect and requires every declared string field to surface as a param, exempting `*_id` by a documented family rule (no template reads an id — ids ride in `deep_link.params`) and everything else only through the explicit `notTemplated` / `unmappedByDesign` maps, each entry carrying its reason and checked in both directions, so an exemption that stops being true fails the build.

Package-level helpers: `ExtractParams`, `RequiredParams`, `OptionalParams`, `IsVerbatim`, `RenderVerbatim`, and the `Reason*` error constants / constructors (`ErrUnknownType`, `ErrMissingParam`, `ErrEmptyPayload`, `ErrEmptyVerbatimText`).

`ValidateBundle(fs.FS) error` is the placeholder-consistency guard that `i18n-catalog`'s `cmd/validate` runs on every PR: it checks that every `{{.placeholder}}` in the source-locale `en.json` is declared for its type (required **or** optional), and every declared `requiredParam` appears in at least one placeholder. Optional params are exempt from that reverse check — a translation may drop the request number from a sentence. A partial bundle (subset of catalog types) is accepted; `ValidateBundleComplete(fs.FS) error` additionally asserts every catalog `NotificationType` has a section present — that's the one `i18n-catalog` CI runs against the real shipped bundle.

**New-key / new-type flow.** Adding a translated string to an *existing* notification type is just an `i18n-catalog` PR (new locale entry under an existing section) — no go-common change needed. Adding a **new in-app notification type** is three coupled steps, strictly ordered (this is rule **F11** from the i18n redesign spec):
1. A go-common PR adds the proto enum pin, the `typeKey`/`requiredParams` entry in `pkg/notifyrender/catalog.go`, and the new `BaselineEN` entry — merges and gets tagged first.
2. The `i18n-catalog` PR carries **both** the new `en` section **and** `go get github.com/STECH-Super-App/go-common@<the new sha>` in the same commit. `i18n-catalog`'s validator runs `ValidateBundleComplete` against its **pinned** go-common version, so a catalog PR that adds the en section without bumping the pin fails with "has no matching NotificationType" — a real incident (2026-07-13, `team_member_left`) that looked like unrelated CI breakage. Skipping the bump is soft at runtime (the compiled-in baseline still covers en; only non-en locales are affected until the catalog PR lands) but hard-red in CI.
3. Consumer services (notification-service, inbox-service) re-pin go-common and redeploy to pick up the new baseline entry and render the new type.
The `Missing` field on `LoadSummary` is the load-time backstop for this flow: a baseline key with no matching overlay `en` entry means step 2 was skipped or hasn't merged yet.

### `pkg/notifyoutbox`

**Price-list refusal (type 175):** `SendPartsPriceListRefused` renders the required `file_name` and `reason`, with optional `shop_display_name` and a nameless fallback. Its channels must be exactly IN_APP and PUSH (no email), enforced by `NOTIFYOUTBOX_PARTS_PRICE_LIST_REFUSED_CHANNEL_SET`; the producer retains tenant addressing, role filter and the `PARTS_PRICE_LIST_REPORT` deep link. Ship the generated contract and go-common consumers before any producer emits the directive. This adds no producer.

Messenger's section-closed notice (176) is a person-addressed, actor-scoped `IN_APP`-only envelope. The appeal-opened/closed notices (177/178) are retired. `PublishDirective` validates their actor, channel, payload and identifiers before writing. The enriched `SendChatMessageReceived` with a section is `PUSH`-only; business messages require the receiving actor in `metadata.acting_tenant_id`, with matching sender/counterpart tenant IDs distinct from it. Personal support uses empty tenant IDs and the support-thread ID as the section, with no business-object requirement. Old chat payloads retain their former channel behavior. `notifyrender.ExtractParams` extracts the object and preview; `Renderer.Render` uses the recipient locale supplied by the dispatcher. Attachment-only preview kinds `PHOTO`, `VIDEO`, `DOCUMENT`, `LOCATION`, `ATTACHMENTS` render in the locale template (archives use `DOCUMENT`); `TEXT` or empty uses the preview. The i18n-catalog overlay must be updated as well as the English baseline, because a legacy overlay can override the new template. No signed attachment URL belongs in these fields.
The one publishing door for `TOPIC_NOTIFICATION_EVENTS`. `PublishDirective(ctx, pub, tx, env)` validates the envelope and then writes the outbox row through the caller's own `outbox.Tx`; direct `outbox.Publisher.PublishProto` to that topic is forbidden. Every rejection is a typed `*errors.AppError` with a `NOTIFYOUTBOX_*` Reason, and **every rejection happens inside the producer's transaction** — the deliberate consequence is that a malformed directive rolls the producer's own domain write back instead of being published as an undeliverable push. Adding a check here is therefore never cosmetic: it is a new way for a business write to fail.

Three tiers of check: envelope shape (`event_id`, `aggregate_id`, `occurred_at`, a specified type, non-empty channels, exactly-one-of `recipient_user_id`/`recipient_tenant_id` where a recipient is required, a deep link when IN_APP is present, a non-nil payload), the per-family channel rules, and the catalog's required-param coverage (IN_APP only — an EMAIL/SMS-only directive uses notification-service's own template contract; a verbatim `PLATFORM_MESSAGE` is asserted non-empty instead).

**The repair channel rule** (`NOTIFYOUTBOX_REPAIR_CHANNEL_SET`). A `NOTIFICATION_TYPE_REPAIR_*` directive must declare exactly `[CHANNEL_IN_APP, CHANNEL_PUSH]` — both, in either order, and nothing else (spec §11, D28). It is recognised by enum-name prefix, not by a list of its values, so a new repair type is guarded the day proto-contracts mints it — which is what happened when the family grew from 31 to 33: review-service's two moderation notices (170 `REPAIR_REVIEW_HIDDEN`, 171 `REPAIR_REVIEW_COMPLAINT_RESOLVED`) are PERSON-addressed — an exception to repair's D28 team addressing, which still governs repair's non-deal notices (rule N of 08.10.2026 covers deal notices only, plus repair's open-posting notices since 09.10.2026, and is not built for repair yet, so for now these two are the only `REPAIR_*` types that are person-addressed) — and the prefix holds them to the pair with no change here. The rule exists because the guard everyone cited was not one: "repair has no rows in notification-service's email/SMS template table" describes a table the dispatcher consults **only for a type it does not recognise**, and it recognises every repair type — an envelope declaring EMAIL would have reached the email sender with the push title and body in it (repair critique 2026-09-23, NOTIF-09). The real guard had been that both producers hard-code the pair, which is a convention, one edit from being untrue. Widening repair to a third channel is a deliberate change to this function, plus the email/SMS baselines that would then have to exist. No other family is touched: rent, delivery and parts directives legitimately take EMAIL and SMS.

⚠ **The prefix is the whole membership test, so the rule is family-wide and cannot be narrowed by adding a type.** A future `NOTIFICATION_TYPE_REPAIR_*` that legitimately needs EMAIL — parts is the precedent: it shipped in-app-only and is not fenced at all, so nothing stopped it widening later — will be refused by this guard the moment it is minted in proto-contracts, and the refusal lands INSIDE the producer's transaction, which rolls that producer's business write back. It will not fail in review or at boot; it will fail on the first directive, in production, as a rolled-back write. That is the deliberate trade (a forgotten repair type is guarded by default, which is the failure mode worth having), but it means widening repair is a TWO-repo change with an ordering: `go-common` must carry the exemption — a second predicate beside `isRepairType`, never a hand-maintained list of the other 33 — and be merged and pinned by both producers BEFORE the new type is emitted. Do not solve it by deleting the prefix check.

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
- `Tracing(opts...)`: server span per request, named `METHOD route-template` (`unmatched` when the router matched nothing), extracts the inbound W3C `traceparent` (**trace context only — never `baggage`**, whatever propagator the process has, SL-24), records `http.status_code`. Costs nothing when `tracing.Init` found no endpoint. Inside the platform the inbound `traceparent` is a trusted remote parent (the gateway → service link). **api-gateway alone passes `WithUntrustedParent()`**: every request becomes a new root, so a caller-set sampled flag cannot force sampling, and the caller's span is kept as a span link.
- `RequestLogger(base, opts...)`: builds the request-scoped child logger (`request_id`, plus `trace_id`/`span_id` when a span is active), stores it on `c.Request().Context()`, and emits **exactly one** INFO access line per request (`route`, `method`, `status`, `duration_ms`). `WithUpstream(fn)` adds the gateway-only `upstream` field. It **replaces** `Logger` at call sites — running both doubles log volume.
  - `request_id` comes from the request **context** first (`requestid.FromContext`, where `requestid.Middleware` parks a value it validated or minted, and where `requestid.UnaryServerInterceptor` parks one that arrived over gRPC) and from the `X-Request-ID` header otherwise. It **never mints one itself** — the id it logs has to be the id the caller can see, and only `requestid.Middleware` puts a minted value on the response and on outbound calls. A service that wires `RequestLogger` alone behaves exactly as it always has: the raw header, logged as it arrived, and no field at all when the header is absent.
- `Metrics()`: observes `http_request_duration_seconds{method, route, status_class}`. The `method` label is clamped to the nine standard verbs (`metrics.MethodLabel`): any other request method is `OTHER`, so a client sending random method names cannot mint a histogram per name (SL-06). Native Echo shape, never `WrapMiddleware`: `c.Path()` exists only on the Echo context, and wrapping `http.ResponseWriter` would drop `http.Flusher` and break the SSE endpoints.
- `MetricsUnaryServerInterceptor()`: observes `grpc_server_handling_seconds{grpc_service, grpc_method, grpc_code}`. Attach with `grpc.ChainUnaryInterceptor`, never `grpc.UnaryInterceptor` (grpc-go allows the latter only once per server).

All three skip the operational routes `/metrics`, `/health`, `/livez`, `/readyz`, and clamp an unmatched route to `unmatched` — otherwise probe traffic and vulnerability scanners mint series and log lines forever.

- `Logger` and `CORS` were **removed** (SL-27): nothing in the fleet called either; `CORS` sent `Access-Control-Allow-Origin: *` together with `Allow-Credentials: true`, and `Logger` logged raw paths and addresses with no `request_id`. Use `RequestLogger`; CORS is the gateway's job.
- `HeaderRequestID`: the correlation header, `X-Request-ID`. An **alias for `requestid.Header`**, which is the single definition — the spelling cannot drift between the middleware that reads it and the package that stamps it. Both names stay exported; this one has importers across the fleet (api-gateway binds its stamping header to it at compile time).
- `CORS`: Handles Cross-Origin Resource Sharing.
- `AuthMiddleware`, `OptionalAuthMiddleware`, `RegistrationAuthMiddleware`, `AdminMiddleware`, `ClientMiddleware`: emit `COMMON_*` reasons via `pkg/errors` + `pkg/response` on rejection. `RegistrationAuthMiddleware` no longer logs anything — it used to write the caller's phone number to the standard logger on every request (SL-12).
- `ParseUUIDParam(c, paramName, reason)`: validates an Echo path parameter with `input.UUID` — only the 36-character hyphenated form (any case) is accepted; braces, `urn:uuid:` and bare 32-hex are refused — and returns it **lower-cased**. On failure returns a 400 `*AppError` with the supplied reason, a message derived from `paramName`, and the `*input.Failure` as its cause. Stops malformed UUIDs at the handler boundary so they never reach the repository layer (where Postgres would reject them with SQLSTATE 22P02 and leak as a 500).

### `pkg/requestid`
The fleet's request-id standard: one header spelling, one validation rule, one context key, one place that mints an id, and the helpers that carry it onward. It sits beside `pkg/middleware` rather than inside it so `pkg/middleware` can import it (`RequestLogger` reads the context value) without a cycle.

A request id is a **diagnostic, not a credential**. Nothing here can fail a request: an absent, over-long or malformed inbound value is **replaced, never refused**, so the package returns no error anywhere and declares no `pkg/errors` Reason.

- `Header` = `X-Request-ID`, `MetadataKey` = `x-request-id` (the gRPC wire form, same relation `LocaleMetadataKey` has to `LocaleHeader`), `MaxLength` = 128.
- `Valid(id)`: 1–128 characters of `[A-Za-z0-9-]` — byte for byte api-gateway's long-standing `^[a-zA-Z0-9\-]{1,128}$`, so adopting this package changes no id the fleet already accepts (a test cross-checks the two). The character set is narrow because the value is written into log lines, echoed into a response header and forwarded onward: a newline or NUL in it is noise in three places and at worst a forged log line, and an unbounded length is a log-volume hazard.
- `New()`: a fresh UUIDv4, what the gateway has always stamped.
- `IntoContext(ctx, id)` / `FromContext(ctx) string`: the carrier. Every **ingress** validates (`Middleware`, `FromGRPCContext`) because that is where bytes this process did not write arrive; `IntoContext` does not, being the low-level primitive where a silent drop would hide a caller's mistake. **Egress forwards whatever is parked** — the receiving service validates on arrival, which is the only place with standing to.
- `Middleware(opts...)`: takes a `Valid` inbound id, else mints one and **re-stamps it onto the request header**, echoes it on the response, and parks it on `c.Request().Context()`. `WithoutGeneration()` keeps api-gateway as the single stamping point (nothing parked, nothing echoed, for an absent **or** malformed value); `WithoutResponseEcho()` keeps it off the response.
- `Inject(ctx, http.Header)`: forwards the parked id on an outbound REST call — what keeps one request a single correlated trail across a hop to another stack (sale-service's `EnsureRequestId` reads it on arrival).
- `FromGRPCContext(ctx)` / `ClientInterceptor()` / `UnaryServerInterceptor()`: the same value over gRPC metadata. Attach the client one with `grpc.WithChainUnaryInterceptor` beside `middleware.LocaleClientInterceptor`, the server one with `grpc.ChainUnaryInterceptor` beside `middleware.MetricsUnaryServerInterceptor`. The server interceptor parks the id for readers and forwards; it does not touch logging, since this library has no gRPC equivalent of `RequestLogger`.

Wire order: `Tracing()` → `requestid.Middleware()` → `RequestLogger` → `Metrics()`. The logger reads the context the moment it runs, so an id parked after it never reaches the access line.

⚠ **This is not `echomiddleware.RequestID`, and must not be replaced by it.** That one writes only the response header and never touches `req.Header`, so a value it mints appears in no log line, no span and no downstream service: someone who copies it out of a response and greps Loki finds nothing and concludes the log pipeline is broken. The re-stamp is the whole difference, and it is what makes minting here safe rather than a decoy — generation only ever fires where no id arrived (a dev tool, a probe, a backend caller that forgot to forward), and there it replaces today's outcome, which is no `request_id` anywhere.

`request_id` is a log **field**, never a Loki label — Loki carries `service` and `level` only, and an id promoted to a label would mint one stream per request.

⚠ **sale-service's PHP twin is slightly wider.** `app/Http/Middleware/EnsureRequestId.php` has had the same shape all along — same 128 cap, validate-or-mint-UUIDv4, re-stamp the request header, echo on the response, `request_id` into the log context — but its rule is `^[A-Za-z0-9._\-]+$`, so it also accepts `.` and `_`. The divergence predates this package (it is api-gateway vs sale-service, not Go vs Go) and is inert in practice: the gateway is the stamping point and mints UUIDs, so no id carrying a dot ever reaches either stack. `Valid` keeps the narrower Go rule rather than widening it, because widening would start preserving ids the gateway has always replaced. Which rule wins fleet-wide is an owner decision, not a silent one.

### `pkg/metrics`
Prometheus instruments and exposition. `Registry` is the **only** registry anything serves — never `promauto` or the prometheus default registerer, which nothing scrapes.

- `MountOn(e *echo.Echo)`: wires `GET /metrics` on an Echo instance. Idempotent — safe across two Echo instances, and safe to call twice on one.
- `StartServer(addr) (stop func(context.Context) error)`: standalone `net/http` listener for services with no public Echo, and the uniform mechanism when metrics live on their own port.
- `HTTPRequestDuration`, `GRPCServerHandling`: the two labelled families, **registered at package init** — never inside a middleware factory or a server constructor, because service tests rebuild those repeatedly in one process and `MustRegister` panics on the second call.
- `DefaultBuckets` (`0.005 … 10`), `StatusClass(code)`, `RouteUnmatched`: the shared label contract. Allowed label keys fleet-wide: `method, route, status_class, grpc_service, grpc_method, grpc_code, topic, group, reason, vertical, sqlstate` (`sqlstate` is `pkg/db/pgerr`'s closed five-value set). Never ids, raw paths, emails or tokens.
- `NewCounter`, `NewGauge`, `NewHistogram`: constructors for **unlabelled** instruments. Labelled families are built with `prometheus.New*Vec` directly.

### `pkg/tracing`
OpenTelemetry tracing setup — one call configures the process.

- `Init(serviceName) (shutdown func(context.Context) error, err error)`: sets the global tracer provider and the W3C **trace-context** propagator. Baggage is deliberately not propagated (SL-24): nothing reads it, a client can send 8 KiB of it, and it would be copied into every outbox row and Kafka message. `outbox.PublishProto` and the `events` consumer use the trace-context propagator explicitly, so the internal request → relay → consumer link survives and baggage cannot enter even if a service installs its own propagator; `PublishProto` also drops a caller-supplied `baggage` header.

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
- `New(log, opts...)`, `WithShutdownBudget(d)` (default `DefaultShutdownBudget` = 25 s, one deadline for every phase, fits the k8s 30 s grace), `WithDrainDelay(d)` (default 0, counts against the budget), `WithWorkerReserve(d)` (default `DefaultWorkerReserve` = 10 s, never more than 40% of the budget): the part of the budget the server phase may **not** use. Phases 0 and 1 are hard-stopped at `budget − reserve`, so one handler or SSE stream that never returns can no longer hold the HTTP drain for the whole budget and leave the outbox relay's in-flight batch, the dispatcher's in-flight message, the Kafka readers and the tracer flush abandoned (SL-05).
- Registration: `HTTP(name, *echo.Echo, addr)`, `GRPC(name, *grpc.Server, net.Listener)`, `Server(name, start, stop)` (escape hatch, no hard stop), `Worker(name, run func(ctx) error)`, `Closer(name, close func(ctx) error)`, `CloserGroup(name, members ...NamedCloser)` (`NamedCloser{Name, Close func(ctx) error}`). `Stopping() <-chan struct{}` closes when shutdown begins — SSE/WebSocket handlers select on it.
- `Run(ctx) error`: starts everything, waits for the trigger — `ctx` done (a signal), **any** server start returning (`http.ErrServerClosed` included), or a worker returning a non-nil error (a worker returning `nil` early is only logged) — then, inside the budget:
  0. `Stopping()` closes, the drain delay elapses;
  1. servers stop **in parallel** (`e.Shutdown` / `GracefulStop`); one still busy at the deadline is hard-stopped (`e.Close` / `Stop`) and logged at Warn;
  2. the **workers' context is cancelled only now** — it derives from `context.WithoutCancel(ctx)`, not from the signal, so consumers and the outbox relay keep running while requests drain — and Run waits for them;
  3. closers run **sequentially in registration order** (not reverse; the order is written in `main`). Recommended: Kafka readers/writers → Redis → DB pool → metrics server → tracer flush. A closer that no longer fits the budget is skipped and reported, never run concurrently with the next. A **`CloserGroup` is one step of that order whose members run concurrently**: the step ends when every member has finished or the budget is spent, and each member is reported on its own as `closer:<group>/<member>` (`ErrCloserFailed`, `ErrPanic`, timeout) without stopping its siblings.

  **Register every Kafka reader and writer in one `CloserGroup("kafka", …)`.** A kafka-go consumer-group `Reader.Close` waits for each partition's in-flight Fetch, a broker long-poll of up to `ReaderConfig.MaxWait` (kafka-go's default 10 s) whose socket read deadline is that same `MaxWait`; cancellation is only seen between fetches. An idle reader therefore takes up to one `MaxWait` to close even after its consumer stopped (measured 7.5–9 s on the local stack), and sequential closers sum it: two readers cost 15.5 s of the 25 s budget, three would overrun it. Grouped, they cost one `MaxWait`. **Build every reader with `MaxWait: events.DefaultReaderMaxWait` (2 s)** — the fleet setting (owner decision 2026-10-05) that shortens that one wait to ~2 s, at the price of a fetch round trip every 2 s on an idle topic; delivery latency is unaffected.

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

### `pkg/kafkawriter`
The one place a service gets a Kafka producer from (SL-01). kafka-go's zero value is `RequiredAcks: RequireNone`: `WriteMessages` returns nil before any broker answered, so a hand-built writer lost events with no error anywhere — seven services set `RequireAll` by hand and seven did not.
- `NewProducer(brokers, opts...) *kafka.Writer` — the outbox relay's writer: no fixed topic (every row names its own), `RequireAll`, `&kafka.Hash{}` balancer (a domain event's key keeps its partition and its order; nil is round-robin and `LeastBytes` ignores the key), the Critical Rule 5 retry triple (`MaxAttempts` 5, `WriteBackoffMin` 100 ms, `WriteBackoffMax` 1 s), synchronous, **no topic auto-creation** (Rule 13), transport `kafkaauth.Transport()`.
- `NewTopicWriter(brokers, topic, opts...)` — the same, fixed to one topic: the DLQ or retry tier of a consumer group (`events.DLQName` / `events.RetryName`).
- Options: `WithCompression(c)`, `WithTransport(rt)`. The safety settings have no option on purpose.
- `Check(w) error` — refuses a nil, `Async` or `RequireNone` writer with a typed 500 `COMMON_KAFKA_WRITER_UNSAFE` (`RequireOne` passes: weaker than `RequireAll`, but it waits for the leader). `MustCheck(who, w)` panics with that error; `events.NewDispatcher` calls it on its DLQ and `WithRetry` writers — which do **not** need a key-hashing balancer, a dead letter has no ordering to keep.
- `CheckProducer(w) error` / `MustCheckProducer(who, w)` — `Check` plus the one setting a **domain-event** producer needs (Critical Rule 13): the balancer must partition by the message key (`*kafka.Hash`, `*kafka.ReferenceHash`, `kafka.CRC32Balancer`, `kafka.Murmur2Balancer`). A nil balancer is round-robin and `LeastBytes` ignores the key, so one aggregate's events would land on different partitions and be consumed out of order. `outbox.New` and `outbox.NewRelay` call it: a writer built by hand without `Balancer: &kafka.Hash{}` now **panics at start-up**; `NewProducer` already sets it.

### `pkg/httpserver`
`New(Config) *echo.Echo` — the fleet's Echo server, **available now, adoption in wave 2.** A bare `echo.New()` has no body limit, no `ReadHeaderTimeout`, answers router 404/405 as `{"message":…}` instead of the response envelope, and its default IP extractor believes a client-sent `X-Forwarded-For`.
- Defaults (every zero `Config` field): `BodyLimit` 1 MiB (`413 COMMON_REQUEST_TOO_LARGE`, also for a chunked body), `ReadHeaderTimeout` 10 s (slowloris), `ReadTimeout` 60 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 1 MiB. `WriteTimeout` has **no** default — a server-wide write deadline kills SSE and streaming responses; set it for a service without streams, or per request with `http.NewResponseController(w).SetWriteDeadline`.
- `c.RealIP()` is the TCP peer (`echo.ExtractIPDirect`): a client-sent `X-Forwarded-For` / `X-Real-IP` is not believed. The caller's address is the gateway's `X-Client-IP`, read with `pkg/clientip` (the server installs `clientip.Middleware`).
- `HTTPErrorHandler` renders every error through `response.JSONError`.
- Middleware, outermost first: `Tracing`, `requestid.Middleware`, `clientip.Middleware`, `RequestLogger(cfg.Logger)`, `Metrics`, `LocaleMiddleware`, body limit, panic recovery. Recovery sits **inside** the logger and the metrics so a panic is still one access line and one 5xx observation; the panic value and stack are logged and never reach the client (`COMMON_INTERNAL`).
- Register it with `lifecycle.App.HTTP`; add `health.Register(e)`, `health.RegisterReadiness(e, app.Stopping())` and routes after.

### `pkg/grpcopts`
House gRPC client options. `ClientIdleTimeout(d)` / `ClientIdleTimeoutFromEnv()` (`GRPC_CLIENT_IDLE_TIMEOUT`, default 30 min) as before, plus (SL-30):
- `UnaryTimeoutInterceptor(d)` / `DefaultCallTimeout(d)` — gives a unary call a deadline of `d` **only when its context has none**; a caller's own deadline, shorter or longer, is never touched. Streams are left alone. `DefaultCallTimeoutFromEnv()` reads `GRPC_CLIENT_CALL_TIMEOUT` (default `DefaultClientCallTimeout` = 10 s; `0s` turns it off; a typo falls back to the default).
- `NewClient(target, opts...)` — `grpc.NewClient` with both defaults already applied, then the caller's options (which win where they overlap); adds no transport credentials. A failure is a typed 500 `COMMON_GRPC_DIAL_FAILED`. `.golangci.strict.yml` bans a bare `grpc.NewClient` / `grpc.Dial` outside this package.

### `pkg/health`
- `Register(e)` — `GET`/`HEAD /health`, constant 200: **liveness**. It must stay 200 while a pod drains, or the kubelet restarts a pod that is only finishing its work.
- `RegisterReadiness(e, stopping <-chan struct{})` — `GET`/`HEAD /readyz`: 200 until `stopping` closes, then 503 `COMMON_SHUTTING_DOWN` (SL-30). Pass `lifecycle.App.Stopping()`, which closes when shutdown begins — before the drain delay — so the load balancer stops routing while the pod is still draining. A nil channel is always ready.

### `pkg/lintguards` and `.golangci.strict.yml` — the mechanical guards
A platform rule go-common implements must not live in prose alone (spec §3.4). Two halves:
- **`.golangci.strict.yml`** — a superset of `.golangci.yml` (kept in step by `TestStrictConfigCoversTheBase`) that adds `forbidigo` / `depguard` bans of the home-made alternative: `kafka.NewWriter` (→ `kafkawriter`), a bare `echo.New()` (→ `httpserver.New`), `signal.Notify` / `NotifyContext` (→ `lifecycle`), `c.RealIP`, `echo.HeaderXForwardedFor` / `HeaderXRealIP` and the `echo.ExtractIPFrom…` helpers, `auth.HeaderRealIP` / `HeaderForwardedFor`, **`http.Request.RemoteAddr`** (the TCP peer is the ingress pod, never the visitor — forbidigo matches the field by type, so it fires on any `*http.Request`; a legitimate peer use such as an access log line is marked `//nolint:forbidigo // <reason>`), `clientip.Set` (reserved for the gateway), `http.DefaultClient` / `http.Get` / `Post` / `Head` / `PostForm` (→ `egress`), `grpc.NewClient` / `Dial` / `DialContext` (→ `grpcopts.NewClient`), `c.JSON` (→ `response`), and the standard `log` package (→ `logger`). Test files are exempt; the helper packages themselves are excluded by path. **go-common lints itself with it.** `scripts/check-guards.sh` lints a deliberately bad sample (`pkg/lintguards/testdata/violations`) and fails when any rule stops firing — CI job "Guards fire". (The base file's `c.JSON` pattern `^c\.JSON$` never matched a call: with type analysis forbidigo reads a method as `echo.Context.JSON`; the strict file carries the working form.)
- **Release 2 guards.** forbidigo: `kafka.Reader.FetchMessage` / `ReadMessage` / `CommitMessages` (a hand-written consumer loop → `events.NewDispatcher`), `events.NewMemoryDeduplicator` (tests only), `outbox.NewDeduplicator` (→ `events.NewPostgresDeduplicator`). `lintguards` rules in `DefaultRules()`: `IdempotencyKeyHeaderLiterals` (the literal `"Idempotency-Key"` as the key of a header access — `.Get/.Values/.Peek/.Set/.Add/.Del(...)` or `.Header[...]`; the same word in a CORS allow-list, a config default or an error's field name passes; name the header with `idempotency.Header`), `HandRolledIdempotencyTables` (SQL naming a table called `idempotency_*` or `common_idempotency_keys`), `HandRolledDeduplicator` (SQL naming `processed_events`, `processed_outbox_messages`, …), `HandRolledOutbox` (SQL naming `outbox_messages`), `MemoryDeduplicator`. **The table rules match whole identifiers in statement position** (`FROM`, `INTO`, `UPDATE`, `JOIN`, `TABLE`, `TRUNCATE`, case-insensitive, optional schema and quotes): `orders.idempotency_key`, chat's `client_idempotency_key` and `conversation_processed_events` are not hits, a hand-rolled `idempotency_keys` table is. A service that still has its own old `idempotency_keys` table lists the file that uses it in its `exempt` map with the reason, until it drops the table; **opt-in** (a service adds them when its migration is done): `LegacyEventHandlers()` (bans `events.Handle`) and `NoPoolIn(dirs...)` (bans naming `pgxpool.Pool` in the event-handler packages). Compile-time guards: `events.NewDispatcher` takes the sealed `Deduplicator`; `HandleTx` hands the handler its transaction; `idempotency.Claim` is required by create methods and `Claim.Run` takes a `pgx.Tx`. CI test: `idempotency.CheckRoutes`.
- **`pkg/lintguards`** — the CI-test half, for what a linter cannot tell apart: a `kafka.Writer{…}` *literal* (banned) from a `*kafka.Writer` variable (fine), an `http.Client{…}` literal, and the string literals naming a client-address header — `"X-Forwarded-For"`, `"X-Real-IP"`, `"Forwarded"`, `"X-Forwarded-Host/-Proto/-Port/-Prefix"`, `"X-Original-Forwarded-For"` (kept equal to `trustedheaders.ClientAddress()` by a test; `"X-Client-IP"` itself is allowed). One test per service: `lintguards.Run(t, "../..", lintguards.DefaultRules(), exempt)`, where `exempt` maps a root-relative file (or `dir/`) to the **reason** it is exempt (an empty reason is an error). Test files, `testdata`, `vendor` and hidden directories are not scanned. **`Scan` fails when the root holds no non-test `.go` file** (a wrong relative path must not be a green guard). `scripts/check-guards.sh` also runs the `lintguards` tests over the violations sample, whose tail holds legitimate lookalikes that must not be reported.

**How a service switches the guards on:** change its Makefile's `LINTER_CONFIG_URL` from `…/.golangci.yml` to `…/.golangci.strict.yml` and add the one `lintguards` test. Until it does, nothing changes for it. It then fixes each hit or marks a justified `//nolint:forbidigo // <reason>`. golangci-lint has no warn-only mode (any finding fails the run), so "off" is the base file and "on" is the strict file.


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

Async events across STECH services flow through three cooperating packages — **`pkg/envelope`** (wire contract), **`pkg/outbox`** (producer side, the reaper, and the migrations of every table go-common owns), **`pkg/events`** (consumer side, with the Postgres deduplicator). The split is one-directional: envelope imports nothing repo-local; outbox and events both import envelope; outbox and events never import each other. `pkg/idempotency` (REST) is a separate leaf; its table is created by `outbox.Migrate` and reaped by the outbox reaper.

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

- **Header constants**: `HeaderEventID`, `HeaderEventType`, `HeaderAggregateType`, `HeaderAggregateID`, `HeaderOccurredAt`, `HeaderSchemaVersion`, `HeaderContentType`, `HeaderRetryCount`, `HeaderTraceparent`. (The legacy `HeaderOutboxID` is gone.)
- **`traceparent`** carries W3C trace context across the async hop, so one trace spans request → outbox → relay → consumer. It is injected by `PublishProto` from the **producing request's** context and read back by the Dispatcher as a remote parent. No migration was needed: the outbox row already had `headers JSONB`, and the relay copies every entry verbatim.
- **Value constants**: `ContentTypeProtoJSON`, `SchemaVersionV1`.
- **`Headers` type** (`map[string]string`) with typed accessors: `EventID()`, `EventType()`, `AggregateType()`, `AggregateID()`, `OccurredAt() (time.Time, error)`, `SchemaVersion()`, `ContentType()`, `RetryCount() int`, `Traceparent()`.
- **`FromKafka([]kafka.Header) Headers`** — build typed view from raw Kafka headers.
- **Extractors**: `ExtractEventID` (reads `event_id`), `ExtractEventType`. (`ExtractOutboxID` and the `outbox_id` fallback are gone.)

### `pkg/outbox`
Transactional Outbox Pattern: an event is written in the **same transaction** as the state change it describes, and a relay forwards it to Kafka. At-least-once; the consumer's deduplicator (`pkg/events`) makes the repeat harmless.

- `New(pool, kafkaWriter, logger, cfg, defaultTopic)`: the whole outbox subsystem. **Panics when `kafkaWriter` is `Async`, `RequireNone` or does not partition by key** (`kafkawriter.CheckProducer`) — build it with `kafkawriter.NewProducer`. `NewRelay` does the same check. `New` also wires the by-topic backlog gauges into the sampler (`outbox_topic_pending_messages{topic}`, `outbox_topic_oldest_pending_age_seconds{topic}`), tested through `New`.
- `Start(ctx)`: launches the relay, the reaper and the metrics sampler and returns one `stop()`. `Run(ctx) error` is `Start` for a blocking caller — the shape `pkg/lifecycle` registers: `app.Worker("outbox", ob.Run)`.
- `Migrate(postgresURL)`: runs **every table go-common owns** — `outbox_messages`, `processed_outbox_messages` (old claims, read-only now), **`processed_events`** (the consumer deduplicator) and **`common_idempotency_keys`** (`pkg/idempotency`; the `common_` prefix is deliberate: order-, seo- and machinery-service own an `idempotency_keys` table of another shape, and the library never touches it) — from one embedded goose set (`goose_db_version_outbox`). Call it on every boot; **its position relative to the service's own migrations does not matter** — every statement that could meet an object the service created (its own `outbox_messages` and indexes: chat-service's 006, auth-service's 00001) is `IF NOT EXISTS`, tested in both orders. Pods that boot together are serialised by a Postgres advisory lock (polled, never blocked: migration 007 builds its index `CONCURRENTLY`, which waits for every open transaction, so a waiter must not sit inside one); an interrupted concurrent build leaves an INVALID index that `Migrate` drops and rebuilds. A consumer-only service (no outbox of its own) calls it too: the empty `outbox_messages` table costs nothing.
- `RunInTx(ctx, pool, fn)` / `NewTxRunner(pool)`: run a function in a Postgres transaction. `TxAsPgx(tx)` recovers the `pgx.Tx` from the opaque `outbox.Tx`.

**Publishing.** `Publisher.PublishProto(ctx, tx, opts)` is the only producer API: protojson payload (snake_case), the full envelope header set, `event_id` written to the header and the payload field, `traceparent` captured at insert time. **An event larger than the limit fails the request** (SL-03, B-04): the size of key + payload + headers is checked in `PublishProto`; over `OUTBOX_MAX_MESSAGE_BYTES` (default 900000 — Kafka's record limit is 1 MiB, and Postgres stores the payload as `jsonb`, which adds a space after every `:` and `,`) it returns a typed **422 `COMMON_OUTBOX_EVENT_TOO_LARGE`** (`Params`: `event_type`, `size`, `limit`) and nothing enters the outbox. Before, such a row could never be sent and stalled the whole service's outbox. **The size is exact:** a cheap check on the compact JSON runs first (a lower bound), then `Store.InsertTxLimited` measures what the relay will send — `octet_length(payload::text)` of the jsonb as Postgres prints it, plus key and headers — inside the INSERT statement itself, which is conditional: an oversized row is never inserted, even if the caller swallows the error and commits. If kafka-go still refuses a row (`MessageTooLargeError`, a writer with a smaller `BatchBytes`), the relay logs at Error naming the `event_id` and counts `outbox_topic_message_too_large_total{topic}`; that row holds its topic until a person removes it.

**The relay (release 2: SL-02, SL-03).**
- **Claim, send and mark in ONE transaction** (`Store.ForwardTopic`): the rows are claimed with `FOR UPDATE SKIP LOCKED` *inside* the transaction that marks them sent, so the locks live until the commit. Two relays never CLAIM the same rows concurrently. A relay that crashes between the Kafka write and the commit leaves the rows pending; they are sent again and the consumer's deduplicator absorbs the repeat. The same holds when a write times out: kafka-go may still complete a batch after `WriteMessages` returned, so a timed-out forward can put a row on the broker AND re-send it later — the dedup absorbs that too. No lease column, no attempt counter, no new column.
- **Each topic on its own clock.** `Run` looks at the pending topics every `OUTBOX_POLL_INTERVAL` and starts a forward for every topic that is not already running and not backing off; it never waits for a poll to finish. A topic whose write blocks for `OUTBOX_SEND_TIMEOUT` delays nobody: healthy topics keep their ~1 s cadence. A topic that fails waits out its own backoff (one poll interval, doubling to 30 s) **without holding a slot**; a topic with a full batch is looked at again at once. At most `OUTBOX_TOPIC_CONCURRENCY` (4) topics write at once — **and never more than a quarter of the connection pool** (`max(1, MaxConns/4)`, logged once at Warn when it caps), so a Kafka outage cannot pin the service's database connections. A topic that has failed may take at most all slots but one, so a healthy topic always has a slot (unless the limit is 1); the first time several topics hang at once they all look healthy, so the exposure is one `OUTBOX_SEND_TIMEOUT`. `OUTBOX_BATCH_SIZE` rows per topic per forward, oldest first. **A topic that cannot be written waits, in order — nothing is skipped, nothing is parked — and the other topics keep flowing.**
- **One relay per topic at a time.** A transaction-scoped advisory lock keyed on the topic serialises relays on a topic, so two relays never interleave its rows (`SKIP LOCKED` alone would let the second relay take the *next* rows while the first holds the earlier ones, and send them first). A relay that finds the topic held skips it (`ForwardResult.Busy`), counts `outbox_topic_busy_total{topic}` and, once a topic has been held for longer than any forward can take (2 × `OUTBOX_SEND_TIMEOUT`), logs a Warn — a topic held for ever by a hung relay shows.
- **Per-key order.** Rows go out oldest first by the application clock (`created_at, id`): that is the order of the rows **visible at claim time**. A transaction that inserted an earlier `created_at` but commits later is sent after rows that were already committed (SL-10: there is no per-aggregate sequence column — "no new columns" was the owner's rule for the relay), so a service that needs a strict order between two events of one aggregate must publish them from one transaction or one after the other's commit. If the broker acknowledges a later row while an earlier row of the **same key** failed, the later row is not marked sent (it is re-sent after the earlier one).
- **One `WriteMessages` call is one kafka-go batch.** kafka-go splits a call holding more than the writer's `BatchSize` (100) messages, or more than `BatchBytes` (1 MiB) for one partition, into several batches sent concurrently, and a failed earlier batch does not stop a later one — A2 and A3 could land before A1. The relay sends each topic's rows in chunks that each fit one batch (count ≤ the writer's `BatchSize`, estimated bytes ≤ 90 % of its `BatchBytes`; read from the writer), one call per chunk, and **stops the topic at the first chunk with any failure**.
- **A write is bounded** by `OUTBOX_SEND_TIMEOUT` (30 s) for the whole topic: on expiry the transaction rolls back and the rows are re-sent, so a hung connection never holds a topic's rows. The rollback itself is bounded (5 s).
- **Shutdown flush**: a topic whose rows are already claimed is written and marked on a context detached from the cancel and cut `OUTBOX_SHUTDOWN_FLUSH_TIMEOUT` (5 s) after it; no new topic starts after the cancel, and `Run` returns once the forwards in flight have ended.
- A stuck topic's Error line is repeated at most every 30 s (Debug in between).
- **The topic list is O(topics), not O(pending rows).** `PendingTopics` (every poll) is a loose index scan over `idx_outbox_pending_topic` — one index probe per distinct topic. `PendingStatsByTopic` (every 15 s) counts at most `TopicBacklogCountCap` (10 000) rows per topic — a gauge at the cap means "at least" — and reads the oldest age exactly. Measured on 600 000 pending rows in 6 topics: the old topic list 64 ms (parallel seq scan, 9 300 buffers) against 0.25 ms (49 buffers); the capped stats 8.5 ms on a vacuumed table. The unlabelled `outbox_pending_messages` is still an exact count, O(pending rows), every 15 s.

**The reaper** deletes, in bounded batches (`OUTBOX_PROCESSED_BATCH_SIZE`, at most 100 batches per cycle): sent outbox rows older than `OUTBOX_RETENTION` (72 h); `processed_events` and legacy `processed_outbox_messages` claims older than `OUTBOX_PROCESSED_RETENTION` (720 h — must outlive topic retention + the DLQ replay window); **`common_idempotency_keys` older than `OUTBOX_IDEMPOTENCY_RETENTION` (24 h)**. A missing table makes the reaper stop asking for it, once, at Info. **Every service that has no `outbox.New(...).Run` but uses `events.NewPostgresDeduplicator` (every consumer-only service, not only crm-sync-service) runs the reaper itself** — otherwise its `processed_events` grows by a row per consumed event for ever: `outbox.NewReaper(outbox.NewStore(pool), logger, outbox.DefaultConfig().Reaper)` and `app.Worker("reaper", reaper.Run)`.

**Removed in release 2:** `Store.FetchPending`, `Store.MarkSent`, `Store.MarkSentBatch` (a mark outside the claim's transaction is the bug), the `PollAndForward` internals. **Kept, DEPRECATED:** `outbox.Deduplicator` / `NewDeduplicator` — `events.NewDispatcher` no longer accepts it; use `events.NewPostgresDeduplicator`. The strict lint config bans `outbox.NewDeduplicator`.

**Typed errors** (every one an `*errors.AppError`, 500 unless noted, driver error as cause): `COMMON_OUTBOX_INSERT_FAILED`, `_FETCH_FAILED`, `_STATS_FAILED`, `_MARK_FAILED`, `_CLEANUP_FAILED`, `_TX_FAILED`, `_TX_INVALID`, `_PUBLISH_INVALID`, `_MARSHAL_FAILED`, `_DEDUP_FAILED`, `_MIGRATE_FAILED`, and the 422 `_EVENT_TOO_LARGE`.

**Environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `OUTBOX_POLL_INTERVAL` | `1s` | Relay polling frequency |
| `OUTBOX_BATCH_SIZE` | `100` | Rows per **topic** per poll |
| `OUTBOX_TOPIC_CONCURRENCY` | `4` | Topics forwarded at once; each pins one pooled connection while it writes. Capped to `max(1, pool MaxConns / 4)` (logged) |
| `OUTBOX_SEND_TIMEOUT` | `30s` | One topic's Kafka write while its rows are locked |
| `OUTBOX_SHUTDOWN_FLUSH_TIMEOUT` | `5s` | Claimed topic may keep writing + marking after the relay is cancelled |
| `OUTBOX_MAX_MESSAGE_BYTES` | `900000` | Largest event `PublishProto` accepts (key + payload as `jsonb::text` + headers, measured exactly) |
| `OUTBOX_REAPER_INTERVAL` | `5m` | Cleanup schedule |
| `OUTBOX_RETENTION` | `72h` | Sent outbox rows |
| `OUTBOX_PROCESSED_RETENTION` | `720h` | Deduplicator claims (`processed_events`, legacy table) |
| `OUTBOX_IDEMPOTENCY_RETENTION` | `24h` | `common_idempotency_keys` |
| `OUTBOX_PROCESSED_BATCH_SIZE` | `5000` | Rows deleted per statement |
| `OUTBOX_METRICS_INTERVAL` | `15s` | Backlog gauge sampling |

**Metrics** (on `metrics.Registry`):

| Metric | Type | Meaning |
|---|---|---|
| `outbox_pending_messages` | gauge | Rows in `status='pending'` (unlabelled: one panel covers the Go fleet and sale-service's PHP relay) |
| `outbox_oldest_pending_age_seconds` | gauge | Age of the oldest pending row — the global stall signal |
| `outbox_topic_pending_messages{topic}` | gauge | **Pending rows by topic**; 0 once a topic has drained |
| `outbox_topic_oldest_pending_age_seconds{topic}` | gauge | **Age of the oldest pending row by topic — alert on this: it names the stuck topic**; 0 once drained |
| `outbox_last_success_timestamp_seconds` | gauge | Last poll in which every topic was forwarded without error (a stuck topic stops refreshing it) |
| `outbox_relayed_events_total` / `outbox_topic_relayed_events_total{topic}` | counter | Rows forwarded and marked sent |
| `outbox_relay_errors_total` / `outbox_topic_relay_errors_total{topic}` | counter | Polls with a failure / failed attempts on a topic |
| `outbox_reaped_events_total` | counter | Sent rows deleted after retention |

The unlabelled gauges are published from process start, including on an empty table. The by-topic gauges appear when a topic first has a backlog and are set to 0 (not dropped) when it drains.

**Quick Start (producer):**
```go
// In main.go — after the service's own migrations
if err := outbox.Migrate(cfg.Postgres.URL); err != nil { /* fail start-up */ }
ob := outbox.New(pool, kafkawriter.NewProducer(brokers), zlog, outbox.DefaultConfig(), defaultTopic)
app.Worker("outbox", ob.Run) // pkg/lifecycle

// In a use case — atomic domain write + event publish
txRunner.RunInTx(ctx, func(tx outbox.Tx) error {
    if err := repo.WithTx(tx).Create(ctx, entity); err != nil {
        return err
    }
    return ob.Publisher.PublishProto(ctx, tx, outbox.PublishProtoOptions{
        AggregateType: "user",
        AggregateID:   entity.ID,
        Topic:         events.TopicName(eventsv1.Topic_TOPIC_USER_EVENTS),
        Message:       &usersv1.UserRegistered{UserId: entity.ID, Phone: entity.Phone, Name: entity.Name},
    })
})
```

### `pkg/events`
Typed consumer dispatcher: proto-typed handlers keyed on the proto FQN (`event_type` header), protojson decode, **mandatory deduplication, the handler's own transaction, in-place retry and dead letters.**

**Construction.** `NewDispatcher(reader, dlq, dedup, opts...)` — all three positional arguments are required:
- `dedup` is an `events.Deduplicator`, a **sealed** interface (its method is unexported): the only values are `NewPostgresDeduplicator(pool)` (production) and `NewMemoryDeduplicator()` (unit tests; lint-banned outside `_test.go`). A consumer cannot be wired without dedup and cannot bring its own. `nil` panics — a typed nil such as `(*PostgresDeduplicator)(nil)` too. `WithDedup` is gone. Call `dedup.Ready(ctx)` once after `outbox.Migrate` and treat an error as fatal: without the table every message would dead-letter after the retry window.
- **A service with no database of its own gets a small one.** The deduplicator is a Postgres table (`processed_events`, created by `outbox.Migrate`); there is no dispatcher without it. crm-sync-service gets a database for this (owner decision) — one DSN, `outbox.Migrate`, `events.NewPostgresDeduplicator(pool)`, and the reaper (`outbox.NewReaper`) as a worker.
- `dlq` must be a safe writer (`kafkawriter.NewTopicWriter(brokers, events.DLQName(topic, group))`); an `Async`/`RequireNone` `*kafka.Writer` panics.
- **The consumer group is required.** It names the consumer in the dedup table, so a default would let two dispatchers share claims and drop each other's events (the old default `"unknown"`). `NewDispatcher` takes it from the reader when the reader exposes its `GroupID` (a `*kafka.Reader` built with one), and otherwise **panics** unless `WithGroup` is given. An explicit `WithGroup` wins.
- Options: `WithGroup(id)` (the Kafka `GroupID`, **verbatim**: it labels every consumer metric AND names the consumer in the dedup table), `WithLogger(l)`, `WithRetryWindow(d)`, `WithRetryBackoff(initial, max)`, `WithMessageTimeout(d)` (the `HandleTx` attempt), `WithPrepareTimeout(d)` and `WithApplyTimeout(d)` (the two phases of `HandlePrepared`), and the deprecated `WithRetry(w)`, `WithMaxRetries(n)`.

**Handlers.**
- `HandleTx[T](d, func(ctx, tx events.Tx, msg T) error)` — **the form to use.** The dispatcher opens a transaction, records `(group, event_id)` in `processed_events` in it, calls the handler with that transaction and commits **once** when it returns nil. The event's effect and its "processed" mark are atomic (SL-04): a failed commit or a crash leaves neither, and the retry runs the handler on a clean slate. `events.Tx` is `pgx.Tx`, so it satisfies `outbox.Tx`: pass it to repository methods and to `Publisher.PublishProto` and the events the handler publishes commit with it. The handler and everything it calls must write through `tx`; a write through the pool is a second transaction and is not covered, and neither is a non-Postgres effect (an HTTP call stays at-least-once: make it idempotent).
- `Handle[T](d, func(ctx, msg T) error)` — the **legacy form** (DEPRECATED in prose, not with the Go `Deprecated:` marker: staticcheck's SA1019 would fail every service's lint at once, and the fleet moves one service at a time). Still deduplicated, but the handler gets no transaction, so its writes commit separately: a failed commit of the mark runs the handler again. `lintguards.LegacyEventHandlers()` bans it once a service has converted.
- `HandlePrepared[T, P](d, prepare, apply)` — **the two-phase form**, for a handler whose slow work is not a database write and which writes only afterwards (an ffmpeg run, a rate-limited call to a third party). `prepare(ctx, msg) (P, error)` runs with **no transaction and no pooled connection held**; then the dispatcher opens the transaction, claims the event id, calls `apply(ctx, tx, msg, p) error` and commits once. Every guarantee of `HandleTx` holds (effect and mark atomic, `Classify`, in-place retry, dead letters, panic recovery, `ErrLeaveUncommitted`, metrics). **Which form:**

  | the handler… | use |
  |---|---|
  | does database work only (reads, writes, outbox publishes) | `HandleTx` |
  | does slow external work (media tools, HTTP with waits, rate gates) and then writes | `HandlePrepared` |
  | does slow I/O inside the transaction | **never** — `HandleTx` holds an open, idle transaction through it: a pinned pooled connection, held-back vacuum, and a pool that must be sized for the slowest handler |

  Order of events: (1) a read-only "already processed?" check — a redelivery of a processed event skips `prepare` entirely and holds nothing afterwards; (2) `prepare`, bounded by `EVENTS_PREPARE_TIMEOUT` (**10 min**), its error classified like a handler's; (3) the transaction, bounded by `EVENTS_APPLY_TIMEOUT` (**30 s**, counted from the end of `prepare`, so a slow `prepare` never starves it): claim, `apply`, commit. If a concurrent delivery got the claim first, `apply` is skipped and `p` is discarded. `P` is whatever `prepare` hands to `apply`; use `struct{}` when it returns nothing. Works with the legacy-table bridge and with `NewMemoryDeduplicator` (nil `Tx`).

  **The one new obligation: `prepare` must be idempotent**, and safe to run more than once, even at the same time. It runs again after an `apply` that failed or timed out, after a commit that failed, after a crash between the phases, and in two pods when a duplicate is delivered at once (only one applies). **Nothing caches `p` across attempts**: every retry runs `prepare` afresh. Put an effect that is not repeatable in `apply`, or key it so that repeating it is harmless (an object written under a deterministic key, overwritten by the retry).
- Both coexist on one dispatcher. `lintguards.LegacyEventHandlers()` bans only `events.Handle`; `HandleTx` and `HandlePrepared` are both fine.

**What happens to a failed message — `events.Classify(err) Class`**, the platform's ONE rule (SL-11). Dead-lettered **at once** only for:

| class | when |
|---|---|
| `ClassDecode` | the payload does not decode into the handler's proto message |
| `ClassPoisonPill` | the error wraps `ErrPoisonPill` |
| `ClassValidation` | an `*errors.AppError` with status **400 or 422**, or an `*input.Failure`, anywhere in the chain |
| `ClassPanic` | the handler panicked (in `Handle`, `HandleTx`, or either phase of `HandlePrepared`) and the dispatcher recovered it. Not a value a handler returns, so a service's tests never see it from `Classify`; it is in the table so PHP's hand-written copy mirrors it |

**A panic is dead-lettered at once** (owner decision 14.11.2026): no retry in place and none through the legacy retry tier, DLQ `x-dlq-reason=handler_panic`, the stack in an Error log line (`handler panicked; dead-lettering the message without a retry`, field `stack`), the handler's transaction rolled back and its connection released first. The offset is committed only after the dead-letter write succeeded; a failed write stops the consumer like any other (`ErrDeadLetterWriteFailed`). A panic is a bug, and the same message meets it again on every attempt.

**Everything else is `ClassRetryable`** — 404, 409, 403, 5xx, timeouts, an error nobody classified. A 404 is usually an ordering race and a 503 an outage; dead-lettering them turned every short outage into manual replays. A retryable failure is **retried in place**: attempt, wait (500 ms doubling to 15 s, ±20 % jitter), attempt, … until `EVENTS_RETRY_WINDOW` (**2 minutes**) has passed since the first **failure**, then dead-lettered with `x-dlq-reason=max_retries`, an Error log line `retries exhausted; dead-lettering the message` and `events_consumer_dead_lettered_total{reason="max_retries"}`. A handler that knows a 404 is final marks it with `ErrPoisonPill`. The partition waits during the retries: that is the point (order is kept), and the window bounds it. A **shutdown during a retry wait** leaves the message uncommitted (Run returns an error wrapping `ErrLeaveUncommitted`) and it is redelivered after the restart. `ErrLeaveUncommitted` from a handler outranks the table (it was always so). `Classify` is exported so a service's tests can pin what its handlers return and sale-service's PHP consumers can copy the table by hand.

**One attempt has a time limit** (`EVENTS_MESSAGE_TIMEOUT`, 60 s, for `HandleTx`; a `HandlePrepared` message has `EVENTS_PREPARE_TIMEOUT` 10 min for `prepare` and `EVENTS_APPLY_TIMEOUT` 30 s for its transaction): the handler's context carries it as a deadline, covering the handler and its transaction; an attempt that exceeds it fails (typed `COMMON_EVENTS_MESSAGE_TIMEOUT` joined to the handler's error), rolls back and is retried. A stuck handler no longer pins a pooled connection and the partition for ever.

**A failed dead-letter write STOPS the consumer** (SL-00 — this **reverses go-common#38**, June 2026, which accepted losing the message). If the DLQ write (or the legacy retry-tier write) fails, the dispatcher commits nothing, fetches nothing more and `Run` returns an error wrapping `ErrDeadLetterWriteFailed` (typed 500 `COMMON_EVENTS_DEAD_LETTER_WRITE_FAILED`); the stop is permanent. Wire it with `Worker`, which makes the process exit non-zero so Kubernetes restarts it loudly and Kafka redelivers the message:

```go
app.Worker("user-events-consumer", disp.Worker(app.Stopping()))
```

`Worker` returns the dead-letter error always; `ErrLeaveUncommitted` as an error while the process runs (lifecycle takes a worker's `nil` before shutdown as "returned early; continuing", which would leave a pod with a stopped dispatcher whose reader still holds its partitions) and as `nil` once `Stopping()` has closed; `ctx.Err()` on a cancel. The alert is the pod restart plus `events_consumer_failurepath_errors_total`.

**Run.** `disp.Run(ctx)` handles one message at a time. A cancel finishes the message in flight (handler, transaction, dead-letter write and commit run on `context.WithoutCancel(ctx)`, bounded by the message timeout); a closed reader returns `nil` from `Run` (and from `Worker` once the process is stopping — otherwise `Worker` returns an error wrapping `ErrReaderClosed`, 500 `COMMON_EVENTS_READER_CLOSED`: a dead consumer must restart the pod); other fetch errors back off 100 ms → 5 s. `DefaultReaderMaxWait` (2 s) must be set as `kafka.ReaderConfig.MaxWait` on every consumer reader.

**Deprecated retry tier.** `WithRetry(w)` keeps the old behaviour: a retryable failure goes to the retry topic (x-retry-count + 1) at once instead of being retried in place, until `WithMaxRetries` (3) is spent, then the DLQ. The dead-letter-at-once rules still apply (a panic never goes to the retry tier). A service drops `WithRetry` and its retry topic/reader/writer to adopt in-place retry.

**Other.** `TopicName`/`DLQName`/`RetryName`; `ErrPoisonPill`; `ErrLeaveUncommitted`; every DLQ message carries `x-dlq-reason` (`poison_pill`, `unmarshal_error`, `validation`, `max_retries`, `handler_panic`), `x-dlq-error` (truncated), `x-dlq-first-seen-at`, `x-dlq-last-seen-at`. The dedup claim is per `(group, event_id)` (SL-31); an event recorded in the old `processed_outbox_messages` table is treated as processed while that table exists. A message with no `event_id` is processed without a claim (Warn).

**Environment variables:** `EVENTS_RETRY_WINDOW` (`2m`), `EVENTS_RETRY_INITIAL_BACKOFF` (`500ms`), `EVENTS_RETRY_MAX_BACKOFF` (`15s`), `EVENTS_MESSAGE_TIMEOUT` (`60s`, `HandleTx`), `EVENTS_PREPARE_TIMEOUT` (`10m`) and `EVENTS_APPLY_TIMEOUT` (`30s`) (`HandlePrepared`); an option overrides the variable.

**Metrics** (`topic` is read per message; `group` is the `WithGroup` value):

| Metric | Labels | Meaning |
|---|---|---|
| `events_consumer_processed_total` | `topic`, `group` | Handler ran and the offset was committed |
| `events_consumer_handler_failures_total` | `topic`, `group` | A handler attempt errored, panicked or failed to decode |
| `events_consumer_inplace_retries_total` | `topic`, `group` | A failed attempt scheduled for another in-place attempt |
| `events_consumer_retried_total` | `topic`, `group` | Forwarded to the legacy retry topic |
| `events_consumer_dead_lettered_total` | `topic`, `group`, `reason` | Written to the DLQ, by reason. `max_retries` = the retry window ran out: **alert on it** |
| `events_consumer_dedup_hits_total` | `topic`, `group` | Redelivery skipped: already processed |
| `events_consumer_failurepath_errors_total` | `topic`, `group` | **The DLQ/retry write failed: the consumer stopped and the pod restarts.** Nothing is lost |
| `events_consumer_unhandled_total` | `topic`, `group` | Committed without a handler (no handler for its `event_type`, or none sent) |
| `events_consumer_commit_errors_total` | `topic`, `group` | An offset commit failed after the message was handled |

**Tracing + logging:** each message is handled inside a consumer span continuing the producer's trace (remote parent from `traceparent`); log lines carry `event_id`, `topic`, `trace_id`/`span_id`; the same logger is on the handler's context.

**Quick Start (consumer):**
```go
reader := kafka.NewReader(kafka.ReaderConfig{ /* Brokers, GroupID, Topic, … */ MaxWait: events.DefaultReaderMaxWait })
dlq := kafkawriter.NewTopicWriter(brokers, events.DLQName(eventsv1.Topic_TOPIC_USER_EVENTS, "auth"))

disp := events.NewDispatcher(reader, dlq, events.NewPostgresDeduplicator(pool),
    events.WithGroup(cfg.Kafka.GroupID), events.WithLogger(zlog))

events.HandleTx(disp, func(ctx context.Context, tx events.Tx, e *usersv1.UserUpdated) error {
    return sessionService.RevokeAllForUser(ctx, tx, e.UserId) // writes through tx
})

app.Worker("user-events", disp.Worker(app.Stopping()))
```

### `pkg/idempotency`
The platform's one REST idempotency (spec §3.2). It replaces five hand-written variants. Read `doc.go` for the full example.

- **The key lives in the same transaction as the work.** No "in progress" state: an uncommitted request has left no row, a rolled-back one leaves nothing. `Claim.Run(ctx, tx pgx.Tx, work)` takes the transaction, so the compiler refuses a call outside one.
- **Per (caller, endpoint, key)**, with a SHA-256 of the target and body. Same key + same request → the first result (`Outcome{Replayed: true}`, the work does not run). Same key + different request → **409 `COMMON_IDEMPOTENCY_KEY_REUSED`**. Two identical requests at once → the second waits on the unique index, then replays (or runs, if the first rolled back).
- **The row names the record, never the response**: `Resource{Type, ID}`. A replay loads that record and passes it through the normal permission check.
- **Validation** with `pkg/input` (`ParseKey`): 1–128 characters from `[A-Za-z0-9._:~=+/-]`, no surrounding whitespace; missing → 400 `COMMON_IDEMPOTENCY_KEY_REQUIRED`, malformed → 400 `COMMON_IDEMPOTENCY_KEY_INVALID`.
- **Shape of the guard:** `idempotency.Post/Put/Patch/Delete/Add(g, path, handler)` register a write route with the `Require()` middleware (header validated, body read once and restored) and tag it; `ClaimFor(c, callerID)` returns the `Claim` (typed 500 `COMMON_IDEMPOTENCY_ROUTE_UNGUARDED` on an unguarded route); an application method that creates a record takes the `Claim` as a parameter, so a create path without idempotency does not compile; `idempotency.CheckRoutes(t, router, exempt)` is the per-service CI test that fails for a write route not registered through `Add` (or its optional form) unless it is listed with a reason; the strict lint config and `lintguards` ban reading `Idempotency-Key` or keeping idempotency tables by hand.
- **Registration is checked, use is observed.** `CheckRoutes` proves a route is registered as idempotent, not that its handler uses the key. `Require()` therefore also watches whether a `Claim` was **run** (`Claim.Run`; building one with `ClaimFor` is not enough) for the request: a 2xx answer on such a route with no run increments `idempotency_unclaimed_responses_total{route}` and logs at Error. It never turns a committed success into a 500 — the work is done, and a retry is what the key exists to make safe. The CI half is `idempotency.CheckRoutesRun(t, router, build)`: it drives every idempotent route once with the request your `build(method, path)` returns (or skips it with a reason, which is logged) and fails when the counter moved or when the request did not get a 2xx (then it proves nothing). `idempotency.Routes(e)` lists the idempotent routes.
- **Required is the default; optional is for shipped clients only.** A new endpoint takes `idempotency.Post/...` (a missing key is 400). `idempotency.PostOptional/PutOptional/PatchOptional/DeleteOptional/AddOptional` exist for ONE case: an endpoint whose shipped clients (web, mobile builds in the stores) send no key, and would break the day it became required. An **absent** (or empty) header runs the request once with **no idempotency**: no key row, `ClaimFor` returns a **keyless** `Claim` (`claim.Keyless()`) whose `Run` just runs `work` in the transaction (the returned `Resource` may be empty), and the body is neither read nor capped. A **present** header is enforced exactly as on a required route (validated, hashed with the body, replay, reuse 409). Every keyless call increments `idempotency_keyless_requests_total{route}` — when it stays flat, the clients have caught up: make the route required. A keyless call is not an "unclaimed response". `idempotency.CheckOptionalRoutes(t, router, map[string]string{"POST /x": "reason"})` keeps the optional routes an explicit, reviewed list (an optional route not listed, an entry without a reason, or a listed route that is no longer optional all fail); `idempotency.Routes/RequiredRoutes/OptionalRoutes(e)` list them; `CheckRoutes` counts both as guarded; `CheckRoutesRun` drives an optional route **twice** — with a key and without — so `build` is called twice for it and must return a request that can succeed twice.
- **Body limits (`WithMaxBody`).** `Require` hashes the body before the handler runs, so a KEYED request is capped: **1 MiB by default** (413 `COMMON_IDEMPOTENCY_BODY_TOO_LARGE`). An upload route raises it per route: `idempotency.With(idempotency.WithMaxBody(10<<20)).Post(g, "/machinery/:id/photos", h)` (1 byte … `MaxBodyLimit` = 64 MiB; anything else panics at registration; the server's own `httpserver` body limit applies first, raise it for that group too). **Memory, stated plainly:** the body is read **once**, in chunks that grow as the bytes arrive (a client that announces a big `Content-Length` and sends nothing costs nothing), hashed while it is read, and handed to the handler as a reader over those same chunks — one copy, no growth garbage, no second pass for the hash. The peak is the body size **per concurrent keyed request on that route** (10 MiB cap × 50 simultaneous uploads ≈ 500 MiB): size the cap and the pod for it. Nothing is spooled to disk (a temp dir would need a writable volume in every pod; not done). The hash value is unchanged (SHA-256 of target, `0`, body), so keys stored earlier still match.
- **Multipart retries (`WithMultipartHash`).** By default a `multipart/form-data` body is hashed as raw bytes, so a retry that re-encodes the same form with a **new boundary** (most HTTP clients do, on every send) is a different request: 409 `COMMON_IDEMPOTENCY_KEY_REUSED`. `idempotency.With(idempotency.WithMultipartHash())` opts a route into a **canonical hash**: each part's field name, file name, content type and a hash of its content, in order, length-prefixed, boundary ignored. A changed content, field, name, order or an extra part is still a reuse; other part headers are not hashed; a body that is not valid multipart falls back to the raw hash (stricter, never looser). It costs one extra in-memory pass, no copy.
- If `work` fails, `Run` returns its error unchanged and deletes the key row (the caller rolls back). Assumes READ COMMITTED.
- Storage: **`common_idempotency_keys`** (created by `outbox.Migrate`; not `idempotency_keys`, which order-, seo- and machinery-service own with another shape — the library neither reads nor changes it), deleted after `OUTBOX_IDEMPOTENCY_RETENTION` (24 h) by the outbox reaper.
- Reasons (add en/ru texts to i18n-catalog): `COMMON_IDEMPOTENCY_KEY_REQUIRED`, `_KEY_INVALID`, `_KEY_REUSED`, `_BODY_UNREADABLE`, `_BODY_TOO_LARGE`, `_ROUTE_UNGUARDED`, `_RECORD_INCOMPLETE`, `_FAILED`.


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
