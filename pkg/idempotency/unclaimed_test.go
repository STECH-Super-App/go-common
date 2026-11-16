package idempotency_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/STECH-Super-App/go-common/pkg/idempotency"
	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

func unclaimed(t *testing.T, route string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "idempotency_unclaimed_responses_total" {
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

// A service router for these tests: the idempotent routes, a logger on every
// request's context, and a real transaction for the Claim.
func claimRouter(t *testing.T, core zapcore.Core) *echo.Echo {
	t.Helper()
	pool := newPool(t)
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.SetRequest(c.Request().WithContext(logger.IntoContext(c.Request().Context(), zap.New(core))))
			return next(c)
		}
	})
	// Does its work through the claim: the way.
	idempotency.Post(e, "/orders", func(c echo.Context) error {
		claim, err := idempotency.ClaimFor(c, "u1")
		if err != nil {
			return err
		}
		err = inTx(t, pool, func(tx pgx.Tx) error {
			_, err := claim.Run(c.Request().Context(), tx, created("o-"+c.Request().Header.Get(idempotency.Header)))
			return err
		})
		if err != nil {
			return err
		}
		return c.NoContent(http.StatusCreated)
	})
	// Registered as idempotent, but never runs a claim.
	idempotency.Post(e, "/forgetful", func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	// Builds the claim, then forgets to run it.
	idempotency.Post(e, "/half", func(c echo.Context) error {
		if _, err := idempotency.ClaimFor(c, "u1"); err != nil {
			return err
		}
		return c.NoContent(http.StatusOK)
	})
	// Answers a client error: no work done, nothing to claim.
	idempotency.Post(e, "/refusing", func(c echo.Context) error { return c.NoContent(http.StatusUnprocessableEntity) })
	idempotency.Post(e, "/failing", func(echo.Context) error { return echo.NewHTTPError(http.StatusConflict, "no") })
	return e
}

// R7: registration is checked, use is observed. A route registered through
// idempotency.Post whose handler never runs a Claim passed CheckRoutes and
// shipped as "idempotent".
func TestRequire_ASuccessWithoutAClaimIsCountedAndLoggedButNotRefused(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	e := claimRouter(t, core)

	before := unclaimed(t, "POST /forgetful")
	rec := serve(e, http.MethodPost, "/forgetful", "k1", `{}`)

	assert.Equal(t, http.StatusCreated, rec.Code, "a committed success is never turned into a failure")
	assert.Equal(t, before+1, unclaimed(t, "POST /forgetful"))
	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "POST /forgetful", logs.All()[0].ContextMap()["route"])

	// Building the claim is not running it.
	beforeHalf := unclaimed(t, "POST /half")
	assert.Equal(t, http.StatusOK, serve(e, http.MethodPost, "/half", "k2", `{}`).Code)
	assert.Equal(t, beforeHalf+1, unclaimed(t, "POST /half"))
}

func TestRequire_AClaimThatRanIsNotCountedNorAReplay(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	e := claimRouter(t, core)
	before := unclaimed(t, "POST /orders")

	assert.Equal(t, http.StatusCreated, serve(e, http.MethodPost, "/orders", "same", `{}`).Code)
	assert.Equal(t, http.StatusCreated, serve(e, http.MethodPost, "/orders", "same", `{}`).Code, "the replay runs the claim too")

	assert.Equal(t, before, unclaimed(t, "POST /orders"))
	assert.Zero(t, logs.Len())
}

func TestRequire_RefusalsAndErrorsAreNotUnclaimedSuccesses(t *testing.T) {
	e := claimRouter(t, zapcore.NewNopCore())
	for _, tc := range []struct{ path, route string }{
		{"/refusing", "POST /refusing"},
		{"/failing", "POST /failing"},
	} {
		before := unclaimed(t, tc.route)
		serve(e, http.MethodPost, tc.path, "k", `{}`)
		assert.Equal(t, before, unclaimed(t, tc.route), tc.route)
	}
	before := unclaimed(t, "POST /orders")
	assert.Equal(t, http.StatusBadRequest, serve(e, http.MethodPost, "/orders", "", `{}`).Code, "a missing key never reaches the handler")
	assert.Equal(t, before, unclaimed(t, "POST /orders"))
}

// --- CheckRoutesRun, the test helper a service calls ---

// recorder is a testing.TB that records what the helper reports instead of
// failing the test that is checking the helper.
type recorder struct {
	testing.TB
	errs []string
	logs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, sprintf(format, args...))
}
func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, sprintf(format, args...))
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func TestCheckRoutesRun_FailsForARouteThatNeverClaimsAndPassesForOneThatDoes(t *testing.T) {
	e := claimRouter(t, zapcore.NewNopCore())
	rec := &recorder{TB: t}

	idempotency.CheckRoutesRun(rec, e, func(method, path string) idempotency.RouteRun {
		switch path {
		case "/orders", "/forgetful", "/half":
			return idempotency.RouteRun{Request: httptest.NewRequest(method, path, strings.NewReader(`{}`))}
		case "/refusing":
			return idempotency.RouteRun{Skip: "answers 422 by design"}
		}
		return idempotency.RouteRun{}
	})

	joined := strings.Join(rec.errs, "\n")
	assert.Contains(t, joined, "POST /forgetful")
	assert.Contains(t, joined, "POST /half")
	assert.NotContains(t, joined, "POST /orders", "a route that runs its claim passes")
	assert.Contains(t, joined, "no request for route POST /failing", "an unanswered route is a failure too")
	assert.Contains(t, strings.Join(rec.logs, "\n"), "POST /refusing is not driven: answers 422 by design")
}

func TestCheckRoutesRun_ARequestThatIsNotA2xxProvesNothingAndFails(t *testing.T) {
	e := claimRouter(t, zapcore.NewNopCore())
	rec := &recorder{TB: t}

	idempotency.CheckRoutesRun(rec, e, func(method, path string) idempotency.RouteRun {
		if path == "/refusing" {
			return idempotency.RouteRun{Request: httptest.NewRequest(method, path, strings.NewReader(`{}`))}
		}
		return idempotency.RouteRun{Skip: "not under test"}
	})

	require.Len(t, rec.errs, 1)
	assert.Contains(t, rec.errs[0], "answered 422")
	assert.Contains(t, rec.errs[0], "proves nothing")
}

func TestRoutes_ListsOnlyTheIdempotentRoutes(t *testing.T) {
	e := echo.New()
	h := func(c echo.Context) error { return c.NoContent(http.StatusOK) }
	idempotency.Post(e, "/b", h)
	idempotency.Delete(e, "/a/:id", h)
	e.POST("/bare", h)
	e.GET("/read", h)
	assert.Equal(t, []string{"DELETE /a/:id", "POST /b"}, idempotency.Routes(e))
}
