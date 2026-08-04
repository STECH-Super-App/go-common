package authz_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestParseTenantMemberships(t *testing.T) {
	got := authz.ParseTenantMemberships("11111111-1111-1111-1111-111111111111=ADMIN,22222222-2222-2222-2222-222222222222=MANAGER")
	want := []authz.TenantMembership{
		{TenantID: "11111111-1111-1111-1111-111111111111", Role: "ADMIN"},
		{TenantID: "22222222-2222-2222-2222-222222222222", Role: "MANAGER"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if authz.ParseTenantMemberships("") != nil {
		t.Fatal("empty header must be nil")
	}
	if got := authz.ParseTenantMemberships("garbage,x=ADMIN"); len(got) != 1 || got[0].TenantID != "x" {
		t.Fatalf("malformed entries must be skipped, got %v", got)
	}
}

func TestFormatRoundTrip(t *testing.T) {
	ms := []authz.TenantMembership{{TenantID: "a", Role: "ADMIN"}}
	if got := authz.ParseTenantMemberships(authz.FormatTenantMemberships(ms)); !reflect.DeepEqual(got, ms) {
		t.Fatalf("round trip: %v", got)
	}
}

func TestFormatTenantMembershipsEmpty(t *testing.T) {
	if got := authz.FormatTenantMemberships(nil); got != "" {
		t.Fatalf("empty slice must format to empty string, got %q", got)
	}
}

func TestTenantMembershipsFromContext(t *testing.T) {
	c := newContext(t)
	want := []authz.TenantMembership{{TenantID: "t1", Role: "MANAGER"}}
	c.Set(string(auth.ContextKeyTenantMemberships), want)

	got, ok := authz.TenantMembershipsFromContext(c)
	assert.True(t, ok)
	assert.Equal(t, want, got)
}

func TestTenantMembershipsFromContext_Missing(t *testing.T) {
	c := newContext(t)
	got, ok := authz.TenantMembershipsFromContext(c)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func newContext(t *testing.T) echo.Context {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}
