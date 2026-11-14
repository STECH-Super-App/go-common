package idempotency_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/idempotency"
	"github.com/STECH-Super-App/go-common/pkg/internal/testpg"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, pool := testpg.New(t)
	require.NoError(t, outbox.Migrate(url))
	return pool
}

func claim(t *testing.T, caller, endpoint, key, body string) idempotency.Claim {
	t.Helper()
	k, err := idempotency.ParseKey(key)
	require.NoError(t, err)
	c, err := idempotency.NewClaim(k, idempotency.Request{Caller: caller, Endpoint: endpoint, Target: "POST /orders", Body: []byte(body)})
	require.NoError(t, err)
	return c
}

// inTx runs fn in a transaction that commits when fn returns nil.
func inTx(t *testing.T, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func created(id string) func(context.Context) (idempotency.Resource, error) {
	return func(context.Context) (idempotency.Resource, error) {
		return idempotency.Resource{Type: "order", ID: id}, nil
	}
}

func reason(t *testing.T, err error) string {
	t.Helper()
	var ae *commonerrors.AppError
	require.True(t, errors.As(err, &ae), "want a typed error, got %T: %v", err, err)
	return ae.Reason
}

func TestRun_FirstRequestRunsTheWorkAndSameRequestReplaysIt(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	var runs int
	work := func(context.Context) (idempotency.Resource, error) {
		runs++
		return idempotency.Resource{Type: "order", ID: "o-1"}, nil
	}

	var first idempotency.Outcome
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
		first, err = claim(t, "u1", "POST /orders", "key-1", `{"a":1}`).Run(ctx, tx, work)
		return err
	}))
	var again idempotency.Outcome
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
		again, err = claim(t, "u1", "POST /orders", "key-1", `{"a":1}`).Run(ctx, tx, work)
		return err
	}))

	assert.Equal(t, 1, runs, "the work ran once")
	assert.False(t, first.Replayed)
	assert.True(t, again.Replayed)
	assert.Equal(t, idempotency.Resource{Type: "order", ID: "o-1"}, again.Resource, "a replay names the record, not a response")
}

func TestRun_SameKeyDifferentBodyIsRefused(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) error {
		_, err := claim(t, "u1", "POST /orders", "key-1", `{"a":1}`).Run(ctx, tx, created("o-1"))
		return err
	}))

	var ran bool
	err := inTx(t, pool, func(tx pgx.Tx) error {
		_, err := claim(t, "u1", "POST /orders", "key-1", `{"a":2}`).Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
			ran = true
			return idempotency.Resource{Type: "order", ID: "o-2"}, nil
		})
		return err
	})

	assert.Equal(t, idempotency.ReasonKeyReused, reason(t, err))
	assert.True(t, idempotency.IsKeyReused(err))
	var ae *commonerrors.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusConflict, ae.Code)
	assert.False(t, ran)
}

// A key belongs to a caller and an endpoint.
func TestRun_KeysAreScopedByCallerAndEndpoint(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	var runs int
	work := func(context.Context) (idempotency.Resource, error) {
		runs++
		return idempotency.Resource{Type: "order", ID: uuid.NewString()}, nil
	}
	for _, c := range []idempotency.Claim{
		claim(t, "u1", "POST /orders", "key-1", `{}`),
		claim(t, "u2", "POST /orders", "key-1", `{}`),            // another caller
		claim(t, "u1", "POST /orders/:id/accept", "key-1", `{}`), // another endpoint
	} {
		require.NoError(t, inTx(t, pool, func(tx pgx.Tx) error { _, err := c.Run(ctx, tx, work); return err }))
	}
	assert.Equal(t, 3, runs, "each (caller, endpoint, key) runs on its own")
}

// Two identical requests at once: the second waits on the unique index, then
// gets the first result. Eight at once: one runs.
func TestRun_ConcurrentIdenticalRequestsRunTheWorkOnce(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	var runs atomic.Int32
	work := func(context.Context) (idempotency.Resource, error) {
		runs.Add(1)
		time.Sleep(150 * time.Millisecond) // the others collide with the open transaction
		return idempotency.Resource{Type: "order", ID: "o-race"}, nil
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	outcomes := make([]idempotency.Outcome, 8)
	for i := range outcomes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			assert.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
				outcomes[i], err = claim(t, "u1", "POST /orders", "key-race", `{"a":1}`).Run(ctx, tx, work)
				return err
			}))
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, runs.Load())
	replays := 0
	for _, o := range outcomes {
		assert.Equal(t, "o-race", o.Resource.ID, "every caller got the first result")
		if o.Replayed {
			replays++
		}
	}
	assert.Equal(t, 7, replays)
}

// There is no "in progress" state: when the first request fails, its key is
// free again at once and the retry runs.
func TestRun_FailedFirstRequestLeavesNothingAndTheRetryRuns(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	boom := errors.New("domain refused")

	err := inTx(t, pool, func(tx pgx.Tx) error {
		_, err := claim(t, "u1", "POST /orders", "key-1", `{}`).Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
			return idempotency.Resource{}, boom
		})
		return err
	})
	require.ErrorIs(t, err, boom, "the work's own error passes through untouched")
	var rows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM common_idempotency_keys`).Scan(&rows))
	assert.Zero(t, rows)

	var out idempotency.Outcome
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
		out, err = claim(t, "u1", "POST /orders", "key-1", `{}`).Run(ctx, tx, created("o-1"))
		return err
	}))
	assert.False(t, out.Replayed, "the retry is a first request, not a replay")
}

// A waiting duplicate whose first request rolls back runs the work itself.
func TestRun_DuplicateWaitingOnARolledBackRequestRunsTheWork(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	holding, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = inTx(t, pool, func(tx pgx.Tx) error {
			_, err := claim(t, "u1", "POST /orders", "key-1", `{}`).Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
				close(holding)
				<-release
				return idempotency.Resource{}, errors.New("first fails")
			})
			return err
		})
	}()
	<-holding

	done := make(chan idempotency.Outcome, 1)
	go func() {
		var out idempotency.Outcome
		assert.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
			out, err = claim(t, "u1", "POST /orders", "key-1", `{}`).Run(ctx, tx, created("o-2"))
			return err
		}))
		done <- out
	}()
	select {
	case <-done:
		t.Fatal("the duplicate must wait for the first transaction")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)

	out := <-done
	assert.False(t, out.Replayed)
	assert.Equal(t, "o-2", out.Resource.ID)
}

// A caller that ignores the error and commits still leaves the key free.
func TestRun_WorkErrorReleasesTheKeyEvenIfTheCallerCommits(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) error {
		_, err := claim(t, "u1", "POST /orders", "key-1", `{}`).Run(ctx, tx, func(context.Context) (idempotency.Resource, error) {
			return idempotency.Resource{}, errors.New("ignored by a careless caller")
		})
		require.Error(t, err)
		return nil // commits anyway
	}))
	var rows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM common_idempotency_keys`).Scan(&rows))
	assert.Zero(t, rows)
}

func TestRun_WorkThatReturnsNoResourceIsATypedError(t *testing.T) {
	pool := newPool(t)
	err := inTx(t, pool, func(tx pgx.Tx) error {
		_, err := claim(t, "u1", "POST /orders", "key-1", `{}`).Run(context.Background(), tx,
			func(context.Context) (idempotency.Resource, error) { return idempotency.Resource{}, nil })
		return err
	})
	assert.Equal(t, idempotency.ReasonRecordIncomplete, reason(t, err))
}

func TestRun_RefusesAZeroClaimAndANilTransaction(t *testing.T) {
	_, err := idempotency.Claim{}.Run(context.Background(), nil, created("x"))
	assert.Error(t, err)
	_, err = claim(t, "u1", "POST /orders", "key-1", `{}`).Run(context.Background(), nil, created("x"))
	assert.Equal(t, idempotency.ReasonFailed, reason(t, err))
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		reason string // "" = valid
	}{
		{"uuid", uuid.NewString(), ""},
		{"short", "k1", ""},
		{"allowed punctuation", "a.b_c:d~e=f+g/h-i", ""},
		{"128 characters", strings.Repeat("a", 128), ""},
		{"empty", "", idempotency.ReasonKeyRequired},
		{"129 characters", strings.Repeat("a", 129), idempotency.ReasonKeyInvalid},
		{"surrounding space", " abc", idempotency.ReasonKeyInvalid},
		{"inner space", "ab c", idempotency.ReasonKeyInvalid},
		{"newline", "ab\nc", idempotency.ReasonKeyInvalid},
		{"NUL", "ab\x00c", idempotency.ReasonKeyInvalid},
		{"non-ASCII", "ключ", idempotency.ReasonKeyInvalid},
		{"quote", `a"b`, idempotency.ReasonKeyInvalid},
		{"only spaces", "   ", idempotency.ReasonKeyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := idempotency.ParseKey(tc.raw)
			if tc.reason == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.raw, key.String())
				return
			}
			assert.Equal(t, tc.reason, reason(t, err))
			var ae *commonerrors.AppError
			require.True(t, errors.As(err, &ae))
			assert.Equal(t, http.StatusBadRequest, ae.Code)
		})
	}
}

func TestNewClaim_RefusesAnEmptyCallerOrEndpointOrKey(t *testing.T) {
	k, _ := idempotency.ParseKey("k")
	_, err := idempotency.NewClaim(k, idempotency.Request{Endpoint: "POST /x"})
	assert.Error(t, err)
	_, err = idempotency.NewClaim(k, idempotency.Request{Caller: "u"})
	assert.Error(t, err)
	_, err = idempotency.NewClaim(idempotency.Key{}, idempotency.Request{Caller: "u", Endpoint: "e"})
	assert.Error(t, err)
}

// --- Echo glue ---

func serve(e *echo.Echo, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set(idempotency.Header, key)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestRequire_RefusesAMissingOrBadKeyBeforeTheHandlerAndRestoresTheBody(t *testing.T) {
	e := echo.New()
	var seenBody string
	var claimErr error
	idempotency.Post(e, "/orders/:id/accept", func(c echo.Context) error {
		raw, _ := io.ReadAll(c.Request().Body)
		seenBody = string(raw)
		_, claimErr = idempotency.ClaimFor(c, "u1")
		return c.NoContent(http.StatusCreated)
	})

	rec := serve(e, http.MethodPost, "/orders/7/accept", "", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), idempotency.ReasonKeyRequired)

	rec = serve(e, http.MethodPost, "/orders/7/accept", "bad key", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), idempotency.ReasonKeyInvalid)

	rec = serve(e, http.MethodPost, "/orders/7/accept", "good-key", `{"x":1}`)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, `{"x":1}`, seenBody, "the handler can still read the body")
	assert.NoError(t, claimErr)
}

func TestClaimFor_OnAnUnguardedRouteIsATypedProgrammerError(t *testing.T) {
	e := echo.New()
	var claimErr error
	e.POST("/plain", func(c echo.Context) error {
		_, claimErr = idempotency.ClaimFor(c, "u1")
		return c.NoContent(http.StatusOK)
	})
	serve(e, http.MethodPost, "/plain", "k", `{}`)
	assert.Equal(t, idempotency.ReasonRouteUnguarded, reason(t, claimErr))
}

// The route guard: a write route that is not registered through Add fails the
// service's test unless it is exempt with a reason.
func TestCheckRoutes_FindsWriteRoutesWithoutIdempotency(t *testing.T) {
	e := echo.New()
	h := func(c echo.Context) error { return c.NoContent(http.StatusOK) }
	idempotency.Post(e, "/guarded", h)
	e.GET("/read", h)
	e.POST("/bare", h)
	g := e.Group("/admin")
	g.DELETE("/bare-delete", h)
	idempotency.Delete(g, "/guarded-delete", h)
	e.PATCH("/exempt", h)

	missing, bad := idempotency.WriteRoutesWithout(e, map[string]string{
		"PATCH /exempt": "an operator toggle; repeating it is harmless",
		"PUT /blank":    " ",
	})

	assert.Equal(t, []string{"DELETE /admin/bare-delete", "POST /bare"}, missing)
	assert.Equal(t, []string{"PUT /blank"}, bad, "an exemption without a reason is itself a failure")
}
