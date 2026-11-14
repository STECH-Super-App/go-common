package idempotency

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/response"
)

// ctxKey is the echo context key Require stores the validated key and body
// under. ClaimFor is the only reader.
const ctxKey = "go-common/idempotency/seed"

type seed struct {
	key    Key
	target string
	body   []byte
	// ran becomes true when a Claim built from this request runs (Claim.Run).
	ran *atomic.Bool
}

// Require is the Echo middleware of a write route: it validates the
// Idempotency-Key header and reads the body once (restoring it for the handler),
// so a missing or malformed key is refused with a typed 400 before any work.
// Register routes through Add / Post / Put / Patch / Delete, which put it in
// front of the handler and tag the route for CheckRoutes.
//
// Registration is checked (CheckRoutes), USE is observed: Require also watches
// whether the handler ran a Claim. A 2xx answer on a route whose handler never
// ran one — a route registered as idempotent that creates its record without the
// key — moves idempotency_unclaimed_responses_total{route} and logs at error. It
// does not turn the committed success into a failure: the work is done, and a 500
// would invite the very retry the key was meant to make safe. CheckRoutesRun
// drives each route once in a test and fails when the counter moves.
func Require() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key, err := KeyFromRequest(c.Request())
			if err != nil {
				return response.JSONError(c, err)
			}
			body, err := readAndRestoreBody(c.Request())
			if err != nil {
				return response.JSONError(c, err)
			}
			r := c.Request()
			ran := new(atomic.Bool)
			c.Set(ctxKey, seed{key: key, target: r.Method + " " + r.URL.RequestURI(), body: body, ran: ran})
			err = next(c)
			if err == nil && !ran.Load() && c.Response().Committed && isSuccess(c.Response().Status) {
				noteUnclaimed(c)
			}
			return err
		}
	}
}

func isSuccess(status int) bool { return status >= 200 && status < 300 }

// noteUnclaimed accounts for a 2xx answer on an idempotent route whose handler
// never ran a Claim.
func noteUnclaimed(c echo.Context) {
	route := c.Request().Method + " " + c.Path()
	unclaimedResponses.WithLabelValues(route).Inc()
	logger.FromContext(c.Request().Context()).Error(
		"idempotency: a 2xx answer on an idempotent route whose handler never ran a Claim; a retry with the same key would repeat the work",
		zap.String("route", route), zap.Int("status", c.Response().Status))
}

// ClaimFor builds the Claim of the current request. caller is the authenticated
// user id (from the verified token, never the body). It fails with a typed 500
// when the route was not registered through Add: there is no validated key to
// build a claim from, and that is a wiring mistake, not a client's.
func ClaimFor(c echo.Context, caller string) (Claim, error) {
	s, ok := c.Get(ctxKey).(seed)
	if !ok {
		return Claim{}, internal(ReasonRouteUnguarded,
			"idempotency: route "+c.Request().Method+" "+c.Path()+" was not registered with idempotency.Add", nil)
	}
	claim, err := NewClaim(s.key, Request{
		Caller:   caller,
		Endpoint: c.Request().Method + " " + c.Path(),
		Target:   s.target,
		Body:     s.body,
	})
	claim.ran = s.ran
	return claim, err
}

// Registrar is what *echo.Echo and *echo.Group both are.
type Registrar interface {
	Add(method, path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
}

// routeTag prefixes the Name of a route registered through Add; CheckRoutes
// looks for it.
const routeTag = "idempotent:"

// Add registers a write route with Require in front of its handler and tags it.
func Add(r Registrar, method, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	route := r.Add(method, path, h, append([]echo.MiddlewareFunc{Require()}, mw...)...)
	route.Name = routeTag + route.Name
	return route
}

// Post is Add for POST.
func Post(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return Add(r, http.MethodPost, path, h, mw...)
}

// Put is Add for PUT.
func Put(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return Add(r, http.MethodPut, path, h, mw...)
}

// Patch is Add for PATCH.
func Patch(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return Add(r, http.MethodPatch, path, h, mw...)
}

// Delete is Add for DELETE.
func Delete(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return Add(r, http.MethodDelete, path, h, mw...)
}

// WriteRoutesWithout lists, sorted, the POST/PUT/PATCH/DELETE routes of e that
// were not registered through Add, minus the exempt ones. exempt maps
// "METHOD /path" to the REASON the route needs no idempotency key; an empty
// reason is an error of its own, so an exemption cannot be silent.
func WriteRoutesWithout(e *echo.Echo, exempt map[string]string) (missing []string, badExempt []string) {
	for k, reason := range exempt {
		if strings.TrimSpace(reason) == "" {
			badExempt = append(badExempt, k)
		}
	}
	for _, r := range e.Routes() {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		if strings.HasPrefix(r.Name, routeTag) {
			continue
		}
		if _, ok := exempt[r.Method+" "+r.Path]; ok {
			continue
		}
		missing = append(missing, r.Method+" "+r.Path)
	}
	sort.Strings(missing)
	sort.Strings(badExempt)
	return missing, badExempt
}

// CheckRoutes is the CI half of the REGISTRATION guard: one test per service, built over the
// router the service really serves, fails for every write route that is not
// idempotent and not listed — with its reason — in exempt:
//
//	func TestWriteRoutesAreIdempotent(t *testing.T) {
//		idempotency.CheckRoutes(t, newRouter(t), map[string]string{
//			"POST /admin/orders/:id/force-expire": "operator action; repeating it is harmless",
//		})
//	}
func CheckRoutes(t testing.TB, e *echo.Echo, exempt map[string]string) {
	t.Helper()
	missing, bad := WriteRoutesWithout(e, exempt)
	for _, k := range bad {
		t.Errorf("idempotency exemption %q has no reason", k)
	}
	for _, k := range missing {
		t.Errorf("write route %s is not registered with idempotency.Add (or listed in the exempt map with a reason)", k)
	}
}

// Routes lists, sorted, the "METHOD /path" of every route of e registered
// through Add / Post / Put / Patch / Delete.
func Routes(e *echo.Echo) []string {
	var out []string
	for _, r := range e.Routes() {
		if strings.HasPrefix(r.Name, routeTag) {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	sort.Strings(out)
	return out
}

// RouteRun is what the service gives CheckRoutesRun for one idempotent route.
type RouteRun struct {
	// Request drives the route to a 2xx answer: the path with real parameters, a
	// valid body, the caller's credentials. The Idempotency-Key header is added
	// when it is not set.
	Request *http.Request
	// Skip, when not empty, is the reason this route is not driven (it needs a
	// fixture the test cannot build). It is logged, so a skipped route is a
	// visible decision.
	Skip string
}

// CheckRoutesRun is the CI half of the USE guard: CheckRoutes proves a route is
// registered as idempotent; this proves its handler runs the Claim. It drives
// every idempotent route of e once with the request build returns and fails t
// when a route answers 2xx without a Claim having run (the counter
// idempotency_unclaimed_responses_total{route} moved) — or when the request did
// not get a 2xx at all, because then the check proves nothing about the route:
// fix the request, or Skip it with a reason.
//
//	func TestIdempotentRoutesClaim(t *testing.T) {
//		idempotency.CheckRoutesRun(t, newRouter(t), func(method, path string) idempotency.RouteRun {
//			switch method + " " + path {
//			case "POST /orders":
//				return idempotency.RouteRun{Request: authed(httptest.NewRequest("POST", "/orders", body))}
//			}
//			return idempotency.RouteRun{Skip: "needs a seeded order"}
//		})
//	}
func CheckRoutesRun(t testing.TB, e *echo.Echo, build func(method, path string) RouteRun) {
	t.Helper()
	for i, route := range Routes(e) {
		method, path, _ := strings.Cut(route, " ")
		run := build(method, path)
		if run.Skip != "" {
			t.Logf("idempotency: route %s is not driven: %s", route, run.Skip)
			continue
		}
		if run.Request == nil {
			t.Errorf("idempotency: no request for route %s (return a RouteRun with a Request, or a Skip reason)", route)
			continue
		}
		if run.Request.Header.Get(Header) == "" {
			run.Request.Header.Set(Header, "check-routes-"+strconv.Itoa(i))
		}
		before := unclaimedCount(route)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, run.Request)
		switch {
		case !isSuccess(rec.Code):
			t.Errorf("idempotency: route %s answered %d to the check request, so the check proves nothing about its Claim; fix the request or skip the route with a reason: %s",
				route, rec.Code, strings.TrimSpace(rec.Body.String()))
		case unclaimedCount(route) > before:
			t.Errorf("idempotency: route %s answered %d without running a Claim: its handler does its work outside the Idempotency-Key", route, rec.Code)
		}
	}
}
