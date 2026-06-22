package authz

import (
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/labstack/echo/v4"
)

func TestHasMinGlobalRole(t *testing.T) {
	cases := []struct {
		name     string
		rolesCSV string
		min      GlobalRole
		want     bool
	}{
		{"admin satisfies admin", "user,admin", GlobalRoleAdmin, true},
		{"admin satisfies support_agent (inherits)", "user,admin", GlobalRoleSupportAgent, true},
		{"support_agent satisfies support_agent", "user,support_agent", GlobalRoleSupportAgent, true},
		{"support_agent does NOT satisfy admin", "user,support_agent", GlobalRoleAdmin, false},
		{"user does NOT satisfy support_agent", "user", GlobalRoleSupportAgent, false},
		{"user satisfies user", "user", GlobalRoleUser, true},
		{"whitespace tolerated", " admin , user ", GlobalRoleSupportAgent, true},
		{"empty csv fails closed", "", GlobalRoleSupportAgent, false},
		{"unknown role ranks zero", "superuser", GlobalRoleUser, false},
		{"unknown min fails closed", "admin", GlobalRole("root"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasMinGlobalRole(tc.rolesCSV, tc.min); got != tc.want {
				t.Fatalf("HasMinGlobalRole(%q,%q)=%v want %v", tc.rolesCSV, tc.min, got, tc.want)
			}
		})
	}
}

func TestIsValidGlobalRole(t *testing.T) {
	for _, ok := range []string{"user", "support_agent", "admin"} {
		if !IsValidGlobalRole(ok) {
			t.Fatalf("IsValidGlobalRole(%q)=false want true", ok)
		}
	}
	for _, bad := range []string{"", "USER", "root", "operator"} {
		if IsValidGlobalRole(bad) {
			t.Fatalf("IsValidGlobalRole(%q)=true want false", bad)
		}
	}
}

func TestCallerHasMinGlobalRole(t *testing.T) {
	e := echo.New()
	c := e.NewContext(nil, nil)
	c.Set(string(auth.ContextKeyUserRoles), "user,admin")
	if !CallerHasMinGlobalRole(c, GlobalRoleSupportAgent) {
		t.Fatal("admin in context should satisfy support_agent")
	}
}
