package idempotency

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/STECH-Super-App/go-common/pkg/response"
)

// ctxKey is the echo context key Require stores the validated key and body
// under. ClaimFor is the only reader.
const ctxKey = "go-common/idempotency/seed"

type seed struct {
	key    Key
	target string
	body   []byte
}

// Require is the Echo middleware of a write route: it validates the
// Idempotency-Key header and reads the body once (restoring it for the handler),
// so a missing or malformed key is refused with a typed 400 before any work.
// Register routes through Add / Post / Put / Patch / Delete, which put it in
// front of the handler and tag the route for CheckRoutes.
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
			c.Set(ctxKey, seed{key: key, target: r.Method + " " + r.URL.RequestURI(), body: body})
			return next(c)
		}
	}
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
	return NewClaim(s.key, Request{
		Caller:   caller,
		Endpoint: c.Request().Method + " " + c.Path(),
		Target:   s.target,
		Body:     s.body,
	})
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

// CheckRoutes is the CI half of the guard: one test per service, built over the
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
