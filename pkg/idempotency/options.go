package idempotency

import "fmt"

// MaxBodyLimit is the largest body cap WithMaxBody accepts. The body of a keyed
// request is read into memory before the handler runs (see the memory note on
// WithMaxBody), so the cap is a memory lever: it is bounded here, and a number
// above it is a registration-time panic.
const MaxBodyLimit = 64 << 20

// Option shapes one idempotent route: Require, the registrars (With(...).Post,
// PostOptional, ...) and ClaimFromRequest take them.
type Option func(*config)

type config struct {
	optional  bool
	maxBody   int64
	multipart bool
}

func newConfig(opts []Option) config {
	cfg := config{maxBody: MaxBodyBytes}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// Optional makes the key optional on the route. An ABSENT (or empty)
// Idempotency-Key header runs the request once with no idempotency at all: no
// key row is written, ClaimFor yields a keyless Claim whose Run just runs the
// work inside the transaction, and the body is neither read nor capped here. A
// PRESENT header is enforced exactly as on a required route: validated, hashed
// with the body, replayed or refused as a reuse.
//
// Optional exists for ONE case: an endpoint whose shipped clients send no key
// (the old middleware made it optional) and would break the day it becomes
// required. A new endpoint is required — that is the default and the reason the
// guard exists. Every optional call is counted per route in
// idempotency_keyless_requests_total{route}, so a service can see when its
// clients have caught up and flip the route to required; CheckOptionalRoutes
// keeps the list of optional routes explicit and reviewed.
func Optional() Option { return func(c *config) { c.optional = true } }

// WithMaxBody sets the largest body, in bytes, a KEYED request to this route may
// carry; a larger one is refused with 413 COMMON_IDEMPOTENCY_BODY_TOO_LARGE
// before the handler runs. The default is MaxBodyBytes (1 MiB), right for a JSON
// API; an upload route (photos, up to 10 MiB) raises it:
//
//	idempotency.With(idempotency.WithMaxBody(10 << 20)).Post(g, "/machinery/:id/photos", h)
//
// Memory, stated plainly: to hash the body BEFORE the handler runs (the claim is
// inserted ahead of the work), Require reads it in full into memory, once — in
// chunks that grow as the bytes actually arrive (a client that announces a large
// Content-Length and sends nothing costs nothing), hashed as they are read, and
// handed to the handler as a reader over those same chunks. There is no second
// copy and no growth garbage, but the peak is the body size per concurrent keyed
// request on this route: with a 10 MiB cap and 50 simultaneous uploads, about
// 500 MiB. Size the cap and the pod for that. Nothing is spooled to disk.
//
// The server's own limit (httpserver.Config.BodyLimit) applies first: raise it
// at least as high for the route's group, or this number is never reached.
//
// n must be in 1..MaxBodyLimit; anything else panics at registration.
func WithMaxBody(n int64) Option {
	if n <= 0 || n > MaxBodyLimit {
		panic(fmt.Sprintf("idempotency.WithMaxBody(%d): the limit must be between 1 and %d bytes", n, MaxBodyLimit))
	}
	return func(c *config) { c.maxBody = n }
}

// WithMultipartHash makes a multipart/form-data body hash CANONICALLY: the hash
// covers each part's field name, file name, content type and a hash of its
// content, in order, and ignores the boundary. Without it the hash is over the
// raw bytes, and a retry that re-encodes the same form with a new boundary — as
// most HTTP clients do on every send — is a different request: 409
// COMMON_IDEMPOTENCY_KEY_REUSED, which is the opposite of what an upload retry
// needs.
//
// A part that differs in any of those four things, a different part order or an
// extra part is still a different request. Other part headers (for example
// Content-Transfer-Encoding) are NOT hashed. A body that is not valid multipart,
// or a request that is not multipart/form-data, falls back to the raw hash:
// stricter, never looser. The cost is one extra pass over the body in memory;
// no copy is made.
func WithMultipartHash() Option { return func(c *config) { c.multipart = true } }
