package authz_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/authz"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  authz.Memberships
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{
			"single tenant manager",
			"TENANT:t1=MANAGER",
			authz.Memberships{{OwnerType: authz.OwnerTenant, OwnerID: "t1", Role: authz.RoleManager}},
		},
		{
			"two entries",
			"TENANT:t1=MANAGER,USER:u1=ADMIN",
			authz.Memberships{
				{OwnerType: authz.OwnerTenant, OwnerID: "t1", Role: authz.RoleManager},
				{OwnerType: authz.OwnerUser, OwnerID: "u1", Role: authz.RoleAdmin},
			},
		},
		{
			"trailing empty segment",
			"TENANT:t1=OPERATOR,",
			authz.Memberships{{OwnerType: authz.OwnerTenant, OwnerID: "t1", Role: authz.RoleOperator}},
		},
		{"unknown owner type skipped", "FOO:x=MANAGER", nil},
		{"unknown role skipped", "TENANT:t1=OVERLORD", nil},
		{"missing equals skipped", "TENANT:t1MANAGER", nil},
		{"missing colon skipped", "t1=MANAGER", nil},
		{"empty owner id skipped", "TENANT:=MANAGER", nil},
		{"empty role skipped", "TENANT:t1=", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authz.Parse(tc.in)
			assert.Equal(t, tc.out, got)
		})
	}
}

func TestCanAccess_GlobalAdminBypass(t *testing.T) {
	c := newContext(t)
	c.Set(string(auth.ContextKeyUserRoles), "admin")

	assert.True(t, authz.CanAccess(c, authz.OwnerTenant, "any-tenant", authz.RoleManager))
	assert.True(t, authz.CanAccess(c, authz.OwnerUser, "nobody", authz.RoleAdmin))
}

func TestCanAccess_MembershipRankHierarchy(t *testing.T) {
	c := newContext(t)
	c.Set(string(auth.ContextKeyUserRoles), "user")
	c.Set(string(auth.ContextKeyTeamMemberships), authz.Memberships{
		{OwnerType: authz.OwnerTenant, OwnerID: "tenant-1", Role: authz.RoleManager},
	})

	assert.True(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-1", authz.RoleOperator))
	assert.True(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-1", authz.RoleManager))
	assert.False(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-1", authz.RoleAdmin))
}

func TestCanAccess_OwnerMustMatch(t *testing.T) {
	c := newContext(t)
	c.Set(string(auth.ContextKeyUserRoles), "user")
	c.Set(string(auth.ContextKeyTeamMemberships), authz.Memberships{
		{OwnerType: authz.OwnerTenant, OwnerID: "tenant-1", Role: authz.RoleAdmin},
	})

	assert.False(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-2", authz.RoleOperator))
	assert.False(t, authz.CanAccess(c, authz.OwnerUser, "tenant-1", authz.RoleOperator))
}

func TestCanAccess_NoMembershipsAndNotAdmin(t *testing.T) {
	c := newContext(t)
	c.Set(string(auth.ContextKeyUserRoles), "user")

	assert.False(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-1", authz.RoleOperator))
}

func TestCanAccess_UnknownMinRoleReturnsFalse(t *testing.T) {
	c := newContext(t)
	c.Set(string(auth.ContextKeyUserRoles), "user")
	c.Set(string(auth.ContextKeyTeamMemberships), authz.Memberships{
		{OwnerType: authz.OwnerTenant, OwnerID: "tenant-1", Role: authz.RoleAdmin},
	})

	assert.False(t, authz.CanAccess(c, authz.OwnerTenant, "tenant-1", authz.Role("OVERLORD")))
}

func TestMembershipsFromContext(t *testing.T) {
	c := newContext(t)
	want := authz.Memberships{{OwnerType: authz.OwnerTenant, OwnerID: "t1", Role: authz.RoleManager}}
	c.Set(string(auth.ContextKeyTeamMemberships), want)

	got, ok := authz.MembershipsFromContext(c)
	assert.True(t, ok)
	assert.Equal(t, want, got)
}

func TestMembershipsFromContext_Missing(t *testing.T) {
	c := newContext(t)
	got, ok := authz.MembershipsFromContext(c)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestOwnerTypeFromBool(t *testing.T) {
	assert.Equal(t, authz.OwnerTenant, authz.OwnerTypeFromBool(true))
	assert.Equal(t, authz.OwnerUser, authz.OwnerTypeFromBool(false))
}

func newContext(t *testing.T) echo.Context {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}
