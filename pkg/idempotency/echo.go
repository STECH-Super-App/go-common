package idempotency

import (
	"crypto/sha256"
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
	// hash is the request hash, computed while the body was read (see
	// hashRequestBody); the body itself is not kept here.
	hash [sha256.Size]byte
	// keyless: an Optional route called without the header. There is no key and
	// no hash.
	keyless bool
	// ran becomes true when a Claim built from this request runs (Claim.Run).
	ran *atomic.Bool
}

// Require is the Echo middleware of a write route: it validates the
// Idempotency-Key header and reads the body once (hashing it as it reads, and
// restoring it for the handler), so a missing or malformed key is refused with a
// typed 400 before any work. Register routes through Add / Post / Put / Patch /
// Delete (or With(...) for options), which put it in front of the handler and
// tag the route for CheckRoutes.
//
// Options: Optional (an absent key runs the request with no idempotency),
// WithMaxBody (a body cap above the 1 MiB default), WithMultipartHash.
//
// Registration is checked (CheckRoutes), USE is observed: Require also watches
// whether the handler ran a Claim. A 2xx answer on a route whose handler never
// ran one — a route registered as idempotent that creates its record without the
// key — moves idempotency_unclaimed_responses_total{route} and logs at error. It
// does not turn the committed success into a failure: the work is done, and a 500
// would invite the very retry the key was meant to make safe. CheckRoutesRun
// drives each route once in a test and fails when the counter moves. A KEYLESS
// call to an Optional route is not watched (nothing was claimable); it moves
// idempotency_keyless_requests_total{route} instead.
func Require(opts ...Option) echo.MiddlewareFunc {
	cfg := newConfig(opts)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			r := c.Request()
			ran := new(atomic.Bool)
			if cfg.optional && r.Header.Get(Header) == "" {
				keylessRequests.WithLabelValues(r.Method + " " + c.Path()).Inc()
				c.Set(ctxKey, seed{keyless: true, ran: ran})
				return next(c)
			}
			key, err := KeyFromRequest(r)
			if err != nil {
				return response.JSONError(c, err)
			}
			target := r.Method + " " + r.URL.RequestURI()
			hash, err := hashRequestBody(r, target, cfg.maxBody, cfg.multipart)
			if err != nil {
				return response.JSONError(c, err)
			}
			c.Set(ctxKey, seed{key: key, target: target, hash: hash, ran: ran})
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
// build a claim from, and that is a wiring mistake, not a client's. On an
// Optional route called with no key it returns a keyless Claim (Claim.Keyless):
// Run runs the work and records nothing.
func ClaimFor(c echo.Context, caller string) (Claim, error) {
	s, ok := c.Get(ctxKey).(seed)
	if !ok {
		return Claim{}, internal(ReasonRouteUnguarded,
			"idempotency: route "+c.Request().Method+" "+c.Path()+" was not registered with idempotency.Add", nil)
	}
	endpoint := c.Request().Method + " " + c.Path()
	if s.keyless {
		return newKeylessClaim(caller, endpoint, s.ran)
	}
	claim, err := NewClaim(s.key, Request{Caller: caller, Endpoint: endpoint, Target: s.target})
	claim.hash = s.hash
	claim.ran = s.ran
	return claim, err
}

// Registrar is what *echo.Echo and *echo.Group both are.
type Registrar interface {
	Add(method, path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
}

// routeTag prefixes the Name of a route registered through Add, optionalTag the
// Name of one registered through AddOptional; CheckRoutes looks for them. The
// colon matters: "idempotent:" is not a prefix of "idempotent-optional:".
const (
	routeTag    = "idempotent:"
	optionalTag = "idempotent-optional:"
)

// Mode says how a guarded route treats a missing key.
type Mode string

// The two modes of a guarded route.
const (
	// ModeRequired: a missing key is 400 COMMON_IDEMPOTENCY_KEY_REQUIRED. The
	// default, and the only mode a new endpoint should have.
	ModeRequired Mode = "required"
	// ModeOptional: a missing key runs the request once with no idempotency.
	ModeOptional Mode = "optional"
)

// modeOf is the mode a route was registered with; ok is false for an unguarded one.
func modeOf(r *echo.Route) (m Mode, ok bool) {
	switch {
	case strings.HasPrefix(r.Name, routeTag):
		return ModeRequired, true
	case strings.HasPrefix(r.Name, optionalTag):
		return ModeOptional, true
	}
	return "", false
}

// Mount registers write routes with a fixed set of Options; build one with With.
type Mount struct{ cfg []Option }

// With returns a Mount whose routes take opts:
//
//	idempotency.With(idempotency.WithMaxBody(10<<20), idempotency.WithMultipartHash()).
//		Post(g, "/machinery/:id/photos", h.AddPhoto)
//
// Required is the default mode; pass Optional() only for an endpoint whose
// shipped clients send no key (PostOptional is the short form).
func With(opts ...Option) Mount { return Mount{cfg: opts} }

// Add registers a write route with Require in front of its handler and tags it.
func (m Mount) Add(r Registrar, method, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	tag := routeTag
	if newConfig(m.cfg).optional {
		tag = optionalTag
	}
	route := r.Add(method, path, h, append([]echo.MiddlewareFunc{Require(m.cfg...)}, mw...)...)
	route.Name = tag + route.Name
	return route
}

// Post is Add for POST.
func (m Mount) Post(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return m.Add(r, http.MethodPost, path, h, mw...)
}

// Put is Add for PUT.
func (m Mount) Put(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return m.Add(r, http.MethodPut, path, h, mw...)
}

// Patch is Add for PATCH.
func (m Mount) Patch(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return m.Add(r, http.MethodPatch, path, h, mw...)
}

// Delete is Add for DELETE.
func (m Mount) Delete(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return m.Add(r, http.MethodDelete, path, h, mw...)
}

// Add registers a write route with Require in front of its handler and tags it
// REQUIRED: a request without an Idempotency-Key is a 400.
func Add(r Registrar, method, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return With().Add(r, method, path, h, mw...)
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

// AddOptional registers a write route whose key is OPTIONAL (see Optional): an
// absent header runs the request once with no idempotency, a present one is
// enforced as on a required route. For an endpoint with shipped clients that send
// no key — never for a new one. List it in CheckOptionalRoutes with the reason.
func AddOptional(r Registrar, method, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return With(Optional()).Add(r, method, path, h, mw...)
}

// PostOptional is AddOptional for POST.
func PostOptional(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return AddOptional(r, http.MethodPost, path, h, mw...)
}

// PutOptional is AddOptional for PUT.
func PutOptional(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return AddOptional(r, http.MethodPut, path, h, mw...)
}

// PatchOptional is AddOptional for PATCH.
func PatchOptional(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return AddOptional(r, http.MethodPatch, path, h, mw...)
}

// DeleteOptional is AddOptional for DELETE.
func DeleteOptional(r Registrar, path string, h echo.HandlerFunc, mw ...echo.MiddlewareFunc) *echo.Route {
	return AddOptional(r, http.MethodDelete, path, h, mw...)
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
		if _, guarded := modeOf(r); guarded {
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
// through Add / Post / Put / Patch / Delete or their Optional forms.
func Routes(e *echo.Echo) []string { return routesWhere(e, func(Mode) bool { return true }) }

// RequiredRoutes lists, sorted, the guarded routes whose key is required.
func RequiredRoutes(e *echo.Echo) []string {
	return routesWhere(e, func(m Mode) bool { return m == ModeRequired })
}

// OptionalRoutes lists, sorted, the guarded routes whose key is optional.
func OptionalRoutes(e *echo.Echo) []string {
	return routesWhere(e, func(m Mode) bool { return m == ModeOptional })
}

func routesWhere(e *echo.Echo, want func(Mode) bool) []string {
	var out []string
	for _, r := range e.Routes() {
		if m, ok := modeOf(r); ok && want(m) {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	sort.Strings(out)
	return out
}

// CheckOptionalRoutes keeps the list of OPTIONAL routes explicit: optional is the
// exception for an endpoint with shipped clients that send no key, and a route
// must not become optional by an unreviewed one-word change. It fails t for every
// optional route of e not listed in expected, for an entry with no reason, and
// for an entry that is not an optional route any more (flip it to required —
// the point of idempotency_keyless_requests_total — and delete the entry):
//
//	idempotency.CheckOptionalRoutes(t, router, map[string]string{
//		"POST /machinery": "web and mobile builds in the stores send no key; flip once keyless requests are flat",
//	})
func CheckOptionalRoutes(t testing.TB, e *echo.Echo, expected map[string]string) {
	t.Helper()
	optional := map[string]bool{}
	for _, k := range OptionalRoutes(e) {
		optional[k] = true
	}
	var listed []string
	for k := range expected {
		listed = append(listed, k)
	}
	sort.Strings(listed)
	for _, k := range listed {
		switch {
		case strings.TrimSpace(expected[k]) == "":
			t.Errorf("idempotency optional route %q has no reason", k)
		case !optional[k]:
			t.Errorf("idempotency: %q is listed as optional but is not: make the route required (or drop the stale entry)", k)
		}
	}
	for _, k := range OptionalRoutes(e) {
		if _, ok := expected[k]; !ok {
			t.Errorf("idempotency: %s is registered as optional but is not listed with a reason: new endpoints take a required key", k)
		}
	}
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
// An OPTIONAL route is driven twice — with a key, as above, then with none, which
// must also answer 2xx and move idempotency_keyless_requests_total{route} — so
// build is called twice for it and must return a request that can succeed twice
// (a unique field in the body, if the work has a uniqueness rule).
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
	optional := map[string]bool{}
	for _, k := range OptionalRoutes(e) {
		optional[k] = true
	}
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
		driveKeyed(t, e, route, run.Request)

		if !optional[route] {
			continue
		}
		// An optional route is also driven WITHOUT a key: build is called again
		// (a request body is read once), and the header is removed.
		second := build(method, path)
		if second.Request == nil {
			t.Errorf("idempotency: no second request for optional route %s (it is driven with and without a key)", route)
			continue
		}
		second.Request.Header.Del(Header)
		driveKeyless(t, e, route, second.Request)
	}
}

func driveKeyed(t testing.TB, e *echo.Echo, route string, req *http.Request) {
	t.Helper()
	before := unclaimedCount(route)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	switch {
	case !isSuccess(rec.Code):
		t.Errorf("idempotency: route %s answered %d to the check request, so the check proves nothing about its Claim; fix the request or skip the route with a reason: %s",
			route, rec.Code, strings.TrimSpace(rec.Body.String()))
	case unclaimedCount(route) > before:
		t.Errorf("idempotency: route %s answered %d without running a Claim: its handler does its work outside the Idempotency-Key", route, rec.Code)
	}
}

func driveKeyless(t testing.TB, e *echo.Echo, route string, req *http.Request) {
	t.Helper()
	before := keylessCount(route)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	switch {
	case !isSuccess(rec.Code):
		t.Errorf("idempotency: optional route %s answered %d to the request WITHOUT an Idempotency-Key: the shipped clients that send none would break: %s",
			route, rec.Code, strings.TrimSpace(rec.Body.String()))
	case keylessCount(route) != before+1:
		t.Errorf("idempotency: optional route %s did not count the keyless request in idempotency_keyless_requests_total", route)
	}
}
