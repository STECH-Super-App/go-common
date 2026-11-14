package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

// Header is the request header that carries the client's key.
const Header = "Idempotency-Key"

// MaxKeyLen is the longest key accepted, in characters.
const MaxKeyLen = 128

// MaxBodyBytes is the largest request body ClaimFromRequest and Require read to
// hash it. The server's own body limit (httpserver) is lower or equal.
const MaxBodyBytes = 1 << 20

// Reasons of the typed errors this package returns (Critical Rule 7). They need
// en/ru texts in i18n-catalog like every COMMON_ reason.
const (
	// ReasonKeyRequired (400): a write request without the header.
	ReasonKeyRequired = "COMMON_IDEMPOTENCY_KEY_REQUIRED"
	// ReasonKeyInvalid (400): the key is empty after trimming, longer than
	// MaxKeyLen or has a character outside the allowed set.
	ReasonKeyInvalid = "COMMON_IDEMPOTENCY_KEY_INVALID"
	// ReasonKeyReused (409): the key was used before by this caller on this
	// endpoint with a different request.
	ReasonKeyReused = "COMMON_IDEMPOTENCY_KEY_REUSED"
	// ReasonBodyUnreadable (400): the body could not be read to hash it.
	ReasonBodyUnreadable = "COMMON_IDEMPOTENCY_BODY_UNREADABLE"
	// ReasonBodyTooLarge (413): the body is over MaxBodyBytes.
	ReasonBodyTooLarge = "COMMON_IDEMPOTENCY_BODY_TOO_LARGE"
	// ReasonRouteUnguarded (500): ClaimFor ran on a route that was not registered
	// through Add / Require. A programmer error.
	ReasonRouteUnguarded = "COMMON_IDEMPOTENCY_ROUTE_UNGUARDED"
	// ReasonRecordIncomplete (500): the stored key names no record — a work
	// function that returned success without a resource committed it.
	ReasonRecordIncomplete = "COMMON_IDEMPOTENCY_RECORD_INCOMPLETE"
	// ReasonFailed (500): the storage failed.
	ReasonFailed = "COMMON_IDEMPOTENCY_FAILED"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:~=+/-]+$`)

// Key is a validated Idempotency-Key. Only ParseKey and KeyFromRequest build
// one; the zero value is invalid.
type Key struct{ v string }

// String returns the key as the client sent it.
func (k Key) String() string { return k.v }

// ParseKey validates a client-supplied key with pkg/input: 1..MaxKeyLen
// characters from [A-Za-z0-9._:~=+/-] and nothing else, no surrounding
// whitespace (a key is an identifier, not text: it is never cleaned, only
// accepted or refused). Empty is ReasonKeyRequired, the rest ReasonKeyInvalid.
func ParseKey(raw string) (Key, error) {
	if raw == "" {
		return Key{}, commonerrors.New(http.StatusBadRequest).
			Reason(ReasonKeyRequired).
			Message("the Idempotency-Key header is required on this request").
			Build()
	}
	cleaned, failure := input.Line(raw, MaxKeyLen, input.Required())
	if failure != nil || cleaned != raw || !keyPattern.MatchString(raw) {
		b := commonerrors.New(http.StatusBadRequest).
			Reason(ReasonKeyInvalid).
			Message("the Idempotency-Key must be 1-128 characters from A-Z a-z 0-9 . _ : ~ = + / -").
			Params(map[string]any{"max_length": MaxKeyLen})
		if failure != nil {
			b = b.Cause(failure)
		}
		return Key{}, b.Build()
	}
	return Key{v: raw}, nil
}

// KeyFromRequest reads and validates the Idempotency-Key header.
func KeyFromRequest(r *http.Request) (Key, error) { return ParseKey(r.Header.Get(Header)) }

// Request is what identifies one request to Claim: who sent it, to which
// endpoint, and what it asked for.
type Request struct {
	// Caller is the authenticated caller — the user id from the verified token,
	// never a body field. Keys are scoped to it: two callers never see each
	// other's keys.
	Caller string
	// Endpoint is the route pattern ("POST /orders/:id/accept"): a key reused on
	// another endpoint is a different key.
	Endpoint string
	// Target is the concrete request, method and path (and query if it matters):
	// it goes into the hash, so the same key on /orders/1/accept and
	// /orders/2/accept is a reuse, not a replay.
	Target string
	// Body is the raw request body.
	Body []byte
}

// Claim is a request that wants to run once. It is built by NewClaim or
// ClaimFor, applied inside the work's transaction by Run, and required by every
// application method that creates a record: holding one is the compile-time
// proof that the key was read and validated.
type Claim struct {
	key      Key
	caller   string
	endpoint string
	hash     [sha256.Size]byte
	valid    bool
}

// NewClaim builds a Claim from a validated key and the request's identity.
func NewClaim(key Key, req Request) (Claim, error) {
	if key.v == "" {
		return Claim{}, internal(ReasonKeyRequired, "idempotency: NewClaim needs a key from ParseKey", nil)
	}
	if req.Caller == "" || len(req.Caller) > 128 || req.Endpoint == "" || len(req.Endpoint) > 256 {
		return Claim{}, internal(ReasonFailed, "idempotency: Caller (1-128) and Endpoint (1-256) are required", nil)
	}
	h := sha256.New()
	h.Write([]byte(req.Target))
	h.Write([]byte{0})
	h.Write(req.Body)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return Claim{key: key, caller: req.Caller, endpoint: req.Endpoint, hash: sum, valid: true}, nil
}

// ClaimFromRequest reads the header and the body of r (restoring the body for
// the handler) and builds the Claim. Prefer Require + ClaimFor in an Echo
// service; this is for a non-Echo handler.
func ClaimFromRequest(r *http.Request, caller, endpoint string) (Claim, error) {
	key, err := KeyFromRequest(r)
	if err != nil {
		return Claim{}, err
	}
	body, err := readAndRestoreBody(r)
	if err != nil {
		return Claim{}, err
	}
	return NewClaim(key, Request{Caller: caller, Endpoint: endpoint, Target: r.Method + " " + r.URL.RequestURI(), Body: body})
}

func readAndRestoreBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, commonerrors.New(http.StatusBadRequest).
			Reason(ReasonBodyUnreadable).Message("the request body could not be read").Cause(err).Build()
	}
	if len(body) > MaxBodyBytes {
		return nil, commonerrors.New(http.StatusRequestEntityTooLarge).
			Reason(ReasonBodyTooLarge).Message("the request body is too large").Build()
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// Valid reports whether c came from NewClaim / ClaimFor (the zero Claim did not).
func (c Claim) Valid() bool { return c.valid }

// Resource names the record a request created.
type Resource struct {
	// Type is a short noun of the service's own ("order", "rent_request").
	Type string
	// ID is the record's id.
	ID string
}

// Outcome is what Run did.
type Outcome struct {
	// Resource is the record the request created now, or — on a replay — the one
	// the first request created.
	Resource Resource
	// Replayed is true when the key was already used with the same request: the
	// work did NOT run. The caller loads Resource and passes it through the
	// normal permission check; it must not rebuild a stored response.
	Replayed bool
}

// Run applies the claim inside tx and, for the first request with this key, runs
// work in the same transaction.
//
//   - First time: the key row is inserted, work runs, the row is completed with
//     the Resource work returned. The caller commits tx.
//   - Same key, same request, already committed: Outcome{Replayed: true}; work
//     does not run.
//   - Same key, different request: 409 ReasonKeyReused; work does not run.
//   - Same key in flight in another transaction: the INSERT waits on the unique
//     index until that transaction ends, then behaves as one of the two cases
//     above, or as the first time if it rolled back.
//
// If work returns an error, Run returns it as it is and removes the key row it
// had inserted: the caller must roll tx back (RunInTx does), and a caller that
// commits anyway still leaves the key free instead of stranded for 24 hours.
//
// Run assumes READ COMMITTED, Postgres' default and what every service uses.
func (c Claim) Run(ctx context.Context, tx pgx.Tx, work func(ctx context.Context) (Resource, error)) (Outcome, error) {
	if !c.valid {
		return Outcome{}, internal(ReasonKeyRequired, "idempotency: Run on a zero Claim (build it with ClaimFor or NewClaim)", nil)
	}
	if tx == nil {
		return Outcome{}, internal(ReasonFailed, "idempotency: Run needs the work's transaction", nil)
	}

	// Three rounds at most: a conflicting row can be reaped between the INSERT
	// that saw it and the SELECT that reads it, which only means "try again".
	for range 3 {
		inserted, err := c.insert(ctx, tx)
		if err != nil {
			return Outcome{}, err
		}
		if inserted {
			return c.runWork(ctx, tx, work)
		}
		out, found, err := c.replay(ctx, tx)
		if err != nil {
			return Outcome{}, err
		}
		if found {
			return out, nil
		}
	}
	return Outcome{}, internal(ReasonFailed, "idempotency: the key row kept vanishing", nil)
}

func (c Claim) insert(ctx context.Context, tx pgx.Tx) (bool, error) {
	const q = `
		INSERT INTO idempotency_keys (caller, endpoint, key, request_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (caller, endpoint, key) DO NOTHING`
	tag, err := tx.Exec(ctx, q, c.caller, c.endpoint, c.key.v, c.hash[:])
	if err != nil {
		return false, internal(ReasonFailed, "idempotency: claim key", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (c Claim) runWork(ctx context.Context, tx pgx.Tx, work func(context.Context) (Resource, error)) (Outcome, error) {
	res, err := work(ctx)
	if err != nil {
		c.release(ctx, tx)
		return Outcome{}, err
	}
	if res.Type == "" || res.ID == "" || len(res.Type) > 64 || len(res.ID) > 128 {
		c.release(ctx, tx)
		return Outcome{}, internal(ReasonRecordIncomplete,
			"idempotency: work must return the Resource it created (type 1-64, id 1-128 characters)", nil)
	}
	const q = `
		UPDATE idempotency_keys SET resource_type = $4, resource_id = $5
		WHERE caller = $1 AND endpoint = $2 AND key = $3`
	tag, err := tx.Exec(ctx, q, c.caller, c.endpoint, c.key.v, res.Type, res.ID)
	if err != nil {
		return Outcome{}, internal(ReasonFailed, "idempotency: record the created resource", err)
	}
	if tag.RowsAffected() != 1 {
		return Outcome{}, internal(ReasonFailed, "idempotency: the claimed key row is gone", nil)
	}
	return Outcome{Resource: res}, nil
}

// release deletes the key row inside the failed attempt. Best effort: when the
// transaction is already aborted the statement fails, and the rollback the
// caller owes removes the row anyway.
func (c Claim) release(ctx context.Context, tx pgx.Tx) {
	_, _ = tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE caller = $1 AND endpoint = $2 AND key = $3`,
		c.caller, c.endpoint, c.key.v)
}

// replay reads the row a conflicting INSERT saw. found is false when the row is
// gone again.
func (c Claim) replay(ctx context.Context, tx pgx.Tx) (out Outcome, found bool, err error) {
	const q = `
		SELECT request_hash, resource_type, resource_id FROM idempotency_keys
		WHERE caller = $1 AND endpoint = $2 AND key = $3`
	var (
		hash       []byte
		rtype, rid *string
	)
	if err := tx.QueryRow(ctx, q, c.caller, c.endpoint, c.key.v).Scan(&hash, &rtype, &rid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Outcome{}, false, nil
		}
		return Outcome{}, false, internal(ReasonFailed, "idempotency: read stored key", err)
	}
	if !bytes.Equal(hash, c.hash[:]) {
		return Outcome{}, false, commonerrors.New(http.StatusConflict).
			Reason(ReasonKeyReused).
			Message("this Idempotency-Key was already used for a different request").
			Build()
	}
	if rtype == nil || rid == nil {
		return Outcome{}, false, internal(ReasonRecordIncomplete, "idempotency: the stored key names no record", nil)
	}
	return Outcome{Resource: Resource{Type: *rtype, ID: *rid}, Replayed: true}, true, nil
}

func internal(reason, message string, cause error) *commonerrors.AppError {
	return commonerrors.New(http.StatusInternalServerError).Reason(reason).Message(message).Cause(cause).Build()
}

// IsKeyReused reports whether err is the 409 of a key used for a different
// request, for a caller that maps it itself.
func IsKeyReused(err error) bool {
	var app *commonerrors.AppError
	return errors.As(err, &app) && app.Reason == ReasonKeyReused
}
