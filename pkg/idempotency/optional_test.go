package idempotency_test

// Optional keys (an endpoint whose shipped clients send none), per-route body
// limits, the streamed body hash and the opt-in canonical multipart hash.

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/idempotency"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
	"github.com/STECH-Super-App/go-common/pkg/response"
)

// metricValue reads one labelled counter of the shared registry.
func metricValue(t *testing.T, name, route string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "route" && l.GetValue() == route {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func keyless(t *testing.T, route string) float64 {
	return metricValue(t, "idempotency_keyless_requests_total", route)
}

func rows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM common_idempotency_keys`).Scan(&n))
	return n
}

// workHandler is a handler that does its work through the claim and counts how
// often the work ran; it answers 201 with the replay flag in a header.
func workHandler(t *testing.T, pool *pgxpool.Pool, runs *atomic.Int32, sawKeyless *atomic.Bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		claim, err := idempotency.ClaimFor(c, "u1")
		if err != nil {
			return err
		}
		if sawKeyless != nil {
			sawKeyless.Store(claim.Keyless())
		}
		var out idempotency.Outcome
		err = inTx(t, pool, func(tx pgx.Tx) (err error) {
			out, err = claim.Run(c.Request().Context(), tx, func(context.Context) (idempotency.Resource, error) {
				runs.Add(1)
				return idempotency.Resource{Type: "machinery", ID: "m-1"}, nil
			})
			return err
		})
		if err != nil {
			return response.JSONError(c, err)
		}
		if out.Replayed {
			c.Response().Header().Set("X-Replayed", "1")
		}
		return c.NoContent(http.StatusCreated)
	}
}

func do(e *echo.Echo, method, path, key string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if key != "" {
		req.Header.Set(idempotency.Header, key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// --- optional mode ---

func TestOptional_AbsentHeaderRunsOnceWithNoKeyRowAndIsCounted(t *testing.T) {
	pool := newPool(t)
	e := echo.New()
	var runs atomic.Int32
	var sawKeyless atomic.Bool
	idempotency.PostOptional(e, "/machinery", workHandler(t, pool, &runs, &sawKeyless))

	beforeKeyless, beforeUnclaimed := keyless(t, "POST /machinery"), unclaimed(t, "POST /machinery")
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/machinery", "", strings.NewReader(`{"vin":"1"}`), "").Code)
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/machinery", "", strings.NewReader(`{"vin":"1"}`), "").Code)

	assert.True(t, sawKeyless.Load(), "the handler can tell the claim is keyless")
	assert.EqualValues(t, 2, runs.Load(), "no key, no replay: every request runs the work")
	assert.Zero(t, rows(t, pool), "no key row is written")
	assert.Equal(t, beforeKeyless+2, keyless(t, "POST /machinery"), "each keyless call is counted per route")
	assert.Equal(t, beforeUnclaimed, unclaimed(t, "POST /machinery"), "a keyless call to an optional route is not an unclaimed response")
}

func TestOptional_APresentKeyIsEnforcedExactlyAsOnARequiredRoute(t *testing.T) {
	pool := newPool(t)
	e := echo.New()
	var runs atomic.Int32
	idempotency.PostOptional(e, "/machinery", workHandler(t, pool, &runs, nil))
	before := keyless(t, "POST /machinery")

	first := do(e, "POST", "/machinery", "k-1", strings.NewReader(`{"vin":"1"}`), "")
	replay := do(e, "POST", "/machinery", "k-1", strings.NewReader(`{"vin":"1"}`), "")
	reused := do(e, "POST", "/machinery", "k-1", strings.NewReader(`{"vin":"2"}`), "")
	invalid := do(e, "POST", "/machinery", "bad key", strings.NewReader(`{}`), "")

	assert.Equal(t, http.StatusCreated, first.Code)
	assert.Equal(t, http.StatusCreated, replay.Code)
	assert.Equal(t, "1", replay.Header().Get("X-Replayed"))
	assert.EqualValues(t, 1, runs.Load(), "the replay did not run the work")
	assert.Equal(t, http.StatusConflict, reused.Code)
	assert.Contains(t, reused.Body.String(), idempotency.ReasonKeyReused)
	assert.Equal(t, http.StatusBadRequest, invalid.Code)
	assert.Contains(t, invalid.Body.String(), idempotency.ReasonKeyInvalid)
	assert.Equal(t, 1, rows(t, pool))
	assert.Equal(t, before, keyless(t, "POST /machinery"), "a keyed call is not a keyless one")
}

// A key that is present but empty is no key at all (an HTTP client that sends the
// header with no value).
func TestOptional_AnEmptyHeaderValueCountsAsAbsent(t *testing.T) {
	pool := newPool(t)
	e := echo.New()
	var runs atomic.Int32
	idempotency.PostOptional(e, "/machinery", workHandler(t, pool, &runs, nil))
	req := httptest.NewRequest("POST", "/machinery", strings.NewReader(`{}`))
	req.Header.Set(idempotency.Header, "")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.EqualValues(t, 1, runs.Load())
	assert.Zero(t, rows(t, pool))
}

func TestOptional_ARequiredRouteStillRefusesAMissingKeyAndNeverCountsKeyless(t *testing.T) {
	e := echo.New()
	idempotency.Post(e, "/orders", func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	before := keyless(t, "POST /orders")

	rec := do(e, "POST", "/orders", "", strings.NewReader(`{}`), "")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), idempotency.ReasonKeyRequired)
	assert.Equal(t, before, keyless(t, "POST /orders"))
}

// A keyless request is not hashed, so it is not capped either: the idempotency
// layer adds no limit to a client that opted out of it.
func TestOptional_AKeylessBodyIsNotReadOrCapped(t *testing.T) {
	e := echo.New()
	var got int
	idempotency.PostOptional(e, "/upload", func(c echo.Context) error {
		raw, _ := io.ReadAll(c.Request().Body)
		got = len(raw)
		return c.NoContent(http.StatusCreated)
	})
	body := bytes.Repeat([]byte("x"), idempotency.MaxBodyBytes+4096)

	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "", bytes.NewReader(body), "").Code)
	assert.Equal(t, len(body), got)
	// The same body WITH a key is over the default cap.
	assert.Equal(t, http.StatusRequestEntityTooLarge, do(e, "POST", "/upload", "k", bytes.NewReader(body), "").Code)
}

func TestOptional_AnUnclaimedSuccessIsStillCountedWhenAKeyWasSent(t *testing.T) {
	e := echo.New()
	idempotency.PostOptional(e, "/forgetful", func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	before := unclaimed(t, "POST /forgetful")

	do(e, "POST", "/forgetful", "", strings.NewReader(`{}`), "")
	assert.Equal(t, before, unclaimed(t, "POST /forgetful"), "keyless: not counted")
	do(e, "POST", "/forgetful", "k", strings.NewReader(`{}`), "")
	assert.Equal(t, before+1, unclaimed(t, "POST /forgetful"), "keyed: the USE guard still bites")
}

func TestKeylessClaim_RunNeedsATransactionAndReturnsTheWorksError(t *testing.T) {
	pool := newPool(t)
	r := httptest.NewRequest("POST", "/machinery", strings.NewReader(`{}`))
	c, err := idempotency.ClaimFromRequest(r, "u1", "POST /machinery", idempotency.Optional())
	require.NoError(t, err)
	require.True(t, c.Valid())
	require.True(t, c.Keyless())

	_, err = c.Run(context.Background(), nil, created("x"))
	assert.Error(t, err, "a keyless Run still wants the work's transaction")

	boom := io.ErrUnexpectedEOF
	err = inTx(t, pool, func(tx pgx.Tx) error {
		_, err := c.Run(context.Background(), tx, func(context.Context) (idempotency.Resource, error) {
			return idempotency.Resource{}, boom
		})
		return err
	})
	assert.ErrorIs(t, err, boom)

	var out idempotency.Outcome
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) (err error) {
		out, err = c.Run(context.Background(), tx, func(context.Context) (idempotency.Resource, error) {
			return idempotency.Resource{}, nil // an optional route's work may have no resource to name
		})
		return err
	}))
	assert.False(t, out.Replayed)
	assert.Zero(t, rows(t, pool))
}

func TestClaimFromRequest_RequiredByDefaultOptionalOnRequest(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
	_, err := idempotency.ClaimFromRequest(r, "u1", "POST /x")
	assert.Equal(t, idempotency.ReasonKeyRequired, reason(t, err))

	r = httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
	r.Header.Set(idempotency.Header, "bad key")
	_, err = idempotency.ClaimFromRequest(r, "u1", "POST /x", idempotency.Optional())
	assert.Equal(t, idempotency.ReasonKeyInvalid, reason(t, err), "a present key is validated even when optional")

	r = httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
	r.Header.Set(idempotency.Header, "good-key")
	c, err := idempotency.ClaimFromRequest(r, "u1", "POST /x", idempotency.Optional())
	require.NoError(t, err)
	assert.False(t, c.Keyless())
}

// --- the route guards tell the two modes apart ---

func modeRouter() *echo.Echo {
	e := echo.New()
	h := func(c echo.Context) error { return c.NoContent(http.StatusCreated) }
	idempotency.Post(e, "/orders", h)
	idempotency.PostOptional(e, "/machinery", h)
	idempotency.DeleteOptional(e, "/machinery/:id", h)
	idempotency.PutOptional(e, "/m/:id", h)
	idempotency.PatchOptional(e, "/p/:id", h)
	idempotency.AddOptional(e, http.MethodPost, "/a", h)
	e.POST("/bare", h)
	return e
}

func TestRoutes_ListEveryGuardedRouteAndSplitThemByMode(t *testing.T) {
	e := modeRouter()

	assert.Equal(t, []string{"DELETE /machinery/:id", "PATCH /p/:id", "POST /a", "POST /machinery", "POST /orders", "PUT /m/:id"}, idempotency.Routes(e))
	assert.Equal(t, []string{"POST /orders"}, idempotency.RequiredRoutes(e))
	assert.Equal(t, []string{"DELETE /machinery/:id", "PATCH /p/:id", "POST /a", "POST /machinery", "PUT /m/:id"}, idempotency.OptionalRoutes(e))
}

func TestCheckRoutes_AnOptionalRouteIsGuardedAndABareOneIsNot(t *testing.T) {
	missing, bad := idempotency.WriteRoutesWithout(modeRouter(), nil)
	assert.Equal(t, []string{"POST /bare"}, missing)
	assert.Empty(t, bad)
}

func TestCheckOptionalRoutes_OptionalIsAReviewedListNotASilentChoice(t *testing.T) {
	e := modeRouter()
	rec := &recorder{TB: t}

	idempotency.CheckOptionalRoutes(rec, e, map[string]string{
		"POST /machinery":       "shipped clients send no key; flip once idempotency_keyless_requests_total is flat",
		"DELETE /machinery/:id": " ", // no reason
		"PUT /m/:id":            "shipped clients",
		"PATCH /p/:id":          "shipped clients",
		"POST /orders":          "stale: this route is required", // not optional
	})

	joined := strings.Join(rec.errs, "\n")
	assert.Contains(t, joined, `"DELETE /machinery/:id" has no reason`)
	assert.Contains(t, joined, "POST /a is registered as optional but is not listed")
	assert.Contains(t, joined, `"POST /orders" is listed as optional but is not`)
	assert.NotContains(t, joined, "POST /machinery is registered")
	assert.Len(t, rec.errs, 3)
}

func TestCheckRoutesRun_DrivesAnOptionalRouteBothWithAndWithoutAKey(t *testing.T) {
	pool := newPool(t)
	e := echo.New()
	var runs atomic.Int32
	idempotency.PostOptional(e, "/machinery", workHandler(t, pool, &runs, nil))
	idempotency.Post(e, "/orders", workHandler(t, pool, &runs, nil))
	rec := &recorder{TB: t}
	builds := map[string]int{}

	idempotency.CheckRoutesRun(rec, e, func(method, path string) idempotency.RouteRun {
		builds[path]++
		return idempotency.RouteRun{Request: httptest.NewRequest(method, path, strings.NewReader(`{}`))}
	})

	assert.Empty(t, rec.errs)
	assert.Equal(t, 2, builds["/machinery"], "an optional route is built, and driven, twice")
	assert.Equal(t, 1, builds["/orders"])
	assert.EqualValues(t, 3, runs.Load(), "keyed + keyless on the optional route, keyed on the required one")
}

func TestCheckRoutesRun_FailsAnOptionalRouteThatMishandlesTheKeylessCall(t *testing.T) {
	pool := newPool(t)
	e := echo.New()
	var runs atomic.Int32
	// Works with a key, answers 422 without one: the keyless variant must fail.
	idempotency.PostOptional(e, "/needs-key", func(c echo.Context) error {
		if c.Request().Header.Get(idempotency.Header) == "" {
			return c.NoContent(http.StatusUnprocessableEntity)
		}
		return workHandler(t, pool, &runs, nil)(c)
	})
	// Never claims on the keyed variant.
	idempotency.PostOptional(e, "/forgetful", func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	rec := &recorder{TB: t}

	idempotency.CheckRoutesRun(rec, e, func(method, path string) idempotency.RouteRun {
		return idempotency.RouteRun{Request: httptest.NewRequest(method, path, strings.NewReader(`{}`))}
	})

	joined := strings.Join(rec.errs, "\n")
	assert.Contains(t, joined, "POST /needs-key")
	assert.Contains(t, joined, "WITHOUT an Idempotency-Key")
	assert.Contains(t, joined, "POST /forgetful answered 201 without running a Claim")
}

// --- body limits and the streamed hash ---

func bigRouter(t *testing.T, pool *pgxpool.Pool, runs *atomic.Int32, got *[]byte, opts ...idempotency.Option) *echo.Echo {
	e := echo.New()
	inner := workHandler(t, pool, runs, nil)
	idempotency.With(opts...).Post(e, "/upload", func(c echo.Context) error {
		raw, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		if got != nil {
			*got = raw
		}
		return inner(c)
	})
	return e
}

func TestRequire_TheDefaultCapIsOneMiBAndAnExactMiBPasses(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil)

	over := do(e, "POST", "/upload", "k-over", bytes.NewReader(make([]byte, idempotency.MaxBodyBytes+1)), "")
	exact := do(e, "POST", "/upload", "k-exact", bytes.NewReader(make([]byte, idempotency.MaxBodyBytes)), "")

	assert.Equal(t, http.StatusRequestEntityTooLarge, over.Code)
	assert.Contains(t, over.Body.String(), idempotency.ReasonBodyTooLarge)
	assert.Equal(t, http.StatusCreated, exact.Code)
	assert.EqualValues(t, 1, runs.Load())
}

func TestWithMaxBody_RaisesTheCapOfOneRouteOnly(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	var seen []byte
	e := bigRouter(t, pool, &runs, &seen, idempotency.WithMaxBody(10<<20))
	idempotency.Post(e, "/small", func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	payload := make([]byte, 10<<20)
	for i := range payload {
		payload[i] = byte(i * 7)
	}

	ok := do(e, "POST", "/upload", "k-ten", bytes.NewReader(payload), "")
	assert.Equal(t, http.StatusCreated, ok.Code)
	assert.True(t, bytes.Equal(payload, seen), "the handler reads exactly the bytes that were sent")

	over := do(e, "POST", "/upload", "k-over", bytes.NewReader(append(payload, 1)), "")
	assert.Equal(t, http.StatusRequestEntityTooLarge, over.Code)

	assert.Equal(t, http.StatusRequestEntityTooLarge,
		do(e, "POST", "/small", "k-small", bytes.NewReader(make([]byte, idempotency.MaxBodyBytes+1)), "").Code,
		"another route keeps the default")
}

// A body of unknown length (chunked) is capped too, by reading, not by header.
func TestWithMaxBody_CapsAChunkedBodyWithNoContentLength(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil, idempotency.WithMaxBody(2<<20))
	req := httptest.NewRequest("POST", "/upload", io.NopCloser(bytes.NewReader(make([]byte, 3<<20))))
	req.ContentLength = -1
	req.Header.Set(idempotency.Header, "k-chunked")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Zero(t, runs.Load())
}

func TestWithMaxBody_RefusesNonsenseAtRegistration(t *testing.T) {
	assert.Panics(t, func() { idempotency.WithMaxBody(0) })
	assert.Panics(t, func() { idempotency.WithMaxBody(-1) })
	assert.Panics(t, func() { idempotency.WithMaxBody(idempotency.MaxBodyLimit + 1) }, "an unbounded cap would be a memory lever")
	assert.NotPanics(t, func() { idempotency.WithMaxBody(idempotency.MaxBodyLimit) })
}

// The hash a streamed body gets is the one NewClaim computes over the whole body,
// so keys stored before and after this change compare equal.
func TestRequire_TheStreamedHashEqualsTheOneNewClaimComputes(t *testing.T) {
	pool := newPool(t)
	body := bytes.Repeat([]byte("0123456789"), 90_000) // spans many chunks
	k, err := idempotency.ParseKey("k-compat")
	require.NoError(t, err)
	c, err := idempotency.NewClaim(k, idempotency.Request{Caller: "u1", Endpoint: "POST /upload", Target: "POST /upload", Body: body})
	require.NoError(t, err)
	require.NoError(t, inTx(t, pool, func(tx pgx.Tx) error {
		_, err := c.Run(context.Background(), tx, created("m-1"))
		return err
	}))

	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil, idempotency.WithMaxBody(2<<20))
	rec := do(e, "POST", "/upload", "k-compat", bytes.NewReader(body), "")

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("X-Replayed"), "the same bytes through Require are a replay of the key NewClaim stored")
	assert.Zero(t, runs.Load())
	// And a body one byte different is the 409.
	changed := append(append([]byte(nil), body...), '!')
	assert.Equal(t, http.StatusConflict, do(e, "POST", "/upload", "k-compat", bytes.NewReader(changed), "").Code)
}

// Honest memory: a body is held once. The whole-body ReadAll the first cut used
// allocated several times the body size while it grew; the chunked spool
// allocates about the body once.
func TestRequire_AllocatesAboutTheBodySizeOnceNotSeveralTimes(t *testing.T) {
	const size = 8 << 20
	body := make([]byte, size)
	e := echo.New()
	idempotency.With(idempotency.WithMaxBody(16<<20)).Post(e, "/upload", func(c echo.Context) error {
		n, _ := io.Copy(io.Discard, c.Request().Body)
		assert.EqualValues(t, size, n)
		return c.NoContent(http.StatusCreated)
	})
	req := httptest.NewRequest("POST", "/upload", bytes.NewReader(body))
	req.Header.Set(idempotency.Header, "k-mem")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	e.ServeHTTP(httptest.NewRecorder(), req)
	runtime.ReadMemStats(&after)

	allocated := after.TotalAlloc - before.TotalAlloc
	assert.Less(t, allocated, uint64(size)*3/2, "allocated %d bytes for an %d-byte body", allocated, size)
}

// --- the opt-in canonical multipart hash ---

func multipartBody(t *testing.T, boundary string, parts [][3]string) (body []byte, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.SetBoundary(boundary))
	for _, p := range parts { // name, filename, content
		if p[1] == "" {
			require.NoError(t, w.WriteField(p[0], p[2]))
			continue
		}
		fw, err := w.CreateFormFile(p[0], p[1])
		require.NoError(t, err)
		_, err = fw.Write([]byte(p[2]))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

func TestMultipartHash_ABoundaryChangeIsTheSameRequestOnlyWhenOptedIn(t *testing.T) {
	parts := [][3]string{{"kind", "", "front"}, {"file", "a.jpg", "JPEGBYTES"}}
	bodyA, ctA := multipartBody(t, "AAAAAAAA", parts)
	bodyB, ctB := multipartBody(t, "BBBBBBBB", parts)
	require.NotEqual(t, bodyA, bodyB)

	for _, tc := range []struct {
		name         string
		opts         []idempotency.Option
		wantStatus   int
		wantReplayed bool
	}{
		{"opted in: the retry with another boundary replays", []idempotency.Option{idempotency.WithMultipartHash()}, http.StatusCreated, true},
		{"default: the raw bytes differ, so the key is reused (409)", nil, http.StatusConflict, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newPool(t)
			var runs atomic.Int32
			e := bigRouter(t, pool, &runs, nil, tc.opts...)

			assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-mp", bytes.NewReader(bodyA), ctA).Code)
			second := do(e, "POST", "/upload", "k-mp", bytes.NewReader(bodyB), ctB)

			assert.Equal(t, tc.wantStatus, second.Code)
			assert.Equal(t, tc.wantReplayed, second.Header().Get("X-Replayed") == "1")
			assert.EqualValues(t, 1, runs.Load(), "the work ran once either way")
		})
	}
}

func TestMultipartHash_ADifferentPartIsStillAReusedKey(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil, idempotency.WithMultipartHash())
	base := [][3]string{{"kind", "", "front"}, {"file", "a.jpg", "JPEGBYTES"}}
	bodyA, ctA := multipartBody(t, "AAAA", base)
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-mp2", bytes.NewReader(bodyA), ctA).Code)

	for name, changed := range map[string][][3]string{
		"another file content": {{"kind", "", "front"}, {"file", "a.jpg", "OTHERBYTES"}},
		"another field value":  {{"kind", "", "back"}, {"file", "a.jpg", "JPEGBYTES"}},
		"another file name":    {{"kind", "", "front"}, {"file", "b.jpg", "JPEGBYTES"}},
		"another part name":    {{"kind", "", "front"}, {"photo", "a.jpg", "JPEGBYTES"}},
		"another part order":   {{"file", "a.jpg", "JPEGBYTES"}, {"kind", "", "front"}},
		"an extra part":        {{"kind", "", "front"}, {"file", "a.jpg", "JPEGBYTES"}, {"x", "", "y"}},
	} {
		body, ct := multipartBody(t, "ZZZZ", changed)
		assert.Equal(t, http.StatusConflict, do(e, "POST", "/upload", "k-mp2", bytes.NewReader(body), ct).Code, name)
	}
	assert.EqualValues(t, 1, runs.Load())
}

// Framing: parts that differ only in where the field name ends and the file name
// begins must not collide.
func TestMultipartHash_ValueBoundariesCannotBeShifted(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil, idempotency.WithMultipartHash())
	a, ctA := multipartBody(t, "AAAA", [][3]string{{"ab", "c.jpg", "X"}})
	b, ctB := multipartBody(t, "AAAA", [][3]string{{"a", "bc.jpg", "X"}})
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-frame", bytes.NewReader(a), ctA).Code)
	assert.Equal(t, http.StatusConflict, do(e, "POST", "/upload", "k-frame", bytes.NewReader(b), ctB).Code)
}

// A body that is not valid multipart falls back to the raw hash — stricter, never
// looser — and a non-multipart content type is hashed raw as before.
func TestMultipartHash_FallsBackToTheRawHashWhenTheBodyIsNotMultipart(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	e := bigRouter(t, pool, &runs, nil, idempotency.WithMultipartHash())

	garbage := []byte("this is not multipart at all")
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-bad", bytes.NewReader(garbage), "multipart/form-data; boundary=XX").Code)
	assert.Equal(t, "1", do(e, "POST", "/upload", "k-bad", bytes.NewReader(garbage), "multipart/form-data; boundary=XX").Header().Get("X-Replayed"))
	assert.Equal(t, http.StatusConflict, do(e, "POST", "/upload", "k-bad", bytes.NewReader([]byte("other")), "multipart/form-data; boundary=XX").Code)

	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-json", strings.NewReader(`{"a":1}`), "application/json").Code)
	assert.Equal(t, http.StatusConflict, do(e, "POST", "/upload", "k-json", strings.NewReader(`{"a":2}`), "application/json").Code)
}

// The handler still reads the original bytes after the multipart hash parsed
// them.
func TestMultipartHash_TheHandlerStillGetsTheOriginalBody(t *testing.T) {
	pool := newPool(t)
	var runs atomic.Int32
	var seen []byte
	e := bigRouter(t, pool, &runs, &seen, idempotency.WithMultipartHash())
	body, ct := multipartBody(t, "AAAA", [][3]string{{"file", "a.jpg", strings.Repeat("J", 300_000)}})
	assert.Equal(t, http.StatusCreated, do(e, "POST", "/upload", "k-orig", bytes.NewReader(body), ct).Code)
	assert.True(t, bytes.Equal(body, seen))
}
