package authz_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/authz"
	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
)

const (
	strictTenantA = "11111111-1111-1111-1111-111111111111"
	strictTenantB = "22222222-2222-2222-2222-222222222222"
)

// SL-29: FormatTenantMemberships and the roles CSV did not refuse ',' or '='
// inside a value, so one such value forged or split an entry. Not exploitable
// today (ids are UUIDs, roles are validated on write), but the codec is the
// guard, not its callers.
func TestFormatTenantMemberships_OmitsEntriesTheParserCouldNotReadBack(t *testing.T) {
	got := authz.FormatTenantMemberships([]authz.TenantMembership{
		{TenantID: strictTenantA, Role: "ADMIN"},
		{TenantID: strictTenantB + ",33333333-3333-3333-3333-333333333333=ADMIN", Role: "ADMIN"}, // id forging a second entry
		{TenantID: strictTenantB, Role: "ADMIN,44444444-4444-4444-4444-444444444444=ADMIN"},      // role forging one
		{TenantID: strictTenantB, Role: "A=B"},
		{TenantID: "", Role: "ADMIN"},
		{TenantID: strictTenantB, Role: ""},
		{TenantID: strictTenantB, Role: "MANAGER"},
	})

	assert.Equal(t, strictTenantA+"=ADMIN,"+strictTenantB+"=MANAGER", got)
}

func TestFormatRolesCSV(t *testing.T) {
	assert.Equal(t, "user,admin", authz.FormatRolesCSV([]string{"user", "admin"}))
	assert.Equal(t, "user", authz.FormatRolesCSV([]string{"user", "admin,support_agent", "", " ", "a=b"}),
		"a role that holds a separator, or nothing, is omitted, never split")
	assert.Equal(t, "", authz.FormatRolesCSV(nil))
}

func TestParseTenantMembershipsStrict_Accepts(t *testing.T) {
	got, err := authz.ParseTenantMembershipsStrict(strictTenantA + "=ADMIN," + strings.ToUpper(strictTenantB) + "=MANAGER")
	require.NoError(t, err)
	assert.Equal(t, []authz.TenantMembership{
		{TenantID: strictTenantA, Role: "ADMIN"},
		{TenantID: strictTenantB, Role: "MANAGER"}, // id lower-cased, like the chat headers
	}, got)

	got, err = authz.ParseTenantMembershipsStrict("")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestParseTenantMembershipsStrict_RefusesTheWholeHeader(t *testing.T) {
	bad := map[string]string{
		"not a uuid":        "x=ADMIN",
		"nil uuid":          "00000000-0000-0000-0000-000000000000=ADMIN",
		"no equals":         strictTenantA,
		"empty role":        strictTenantA + "=",
		"empty id":          "=ADMIN",
		"extra field":       strictTenantA + "=ADMIN=X",
		"empty entry":       strictTenantA + "=ADMIN,,",
		"trailing comma":    strictTenantA + "=ADMIN,",
		"duplicate tenant":  strictTenantA + "=ADMIN," + strictTenantA + "=MANAGER",
		"duplicate by case": strictTenantA + "=ADMIN," + strings.ToUpper(strictTenantA) + "=MANAGER",
		"valid then bad":    strictTenantA + "=ADMIN,garbage",
		"role with space":   strictTenantA + "=AD MIN",
		"role with control": strictTenantA + "=AD\x00MIN",
		"too many entries":  strings.Repeat(strictTenantA+"=ADMIN,", authz.MaxMembershipHeaderEntries) + strictTenantB + "=ADMIN",
	}
	for name, header := range bad {
		t.Run(name, func(t *testing.T) {
			got, err := authz.ParseTenantMembershipsStrict(header)

			require.Error(t, err)
			assert.Nil(t, got, "no partial result")
			var ae *apperr.AppError
			require.True(t, errors.As(err, &ae))
			assert.Equal(t, http.StatusBadRequest, ae.Code)
			assert.Equal(t, authz.ReasonTenantMembershipsInvalid, ae.Reason)
		})
	}
}

func TestParseTenantMembershipsStrict_RoundTripsFormat(t *testing.T) {
	ms := []authz.TenantMembership{{TenantID: strictTenantA, Role: "ADMIN"}, {TenantID: strictTenantB, Role: "OPERATOR"}}

	got, err := authz.ParseTenantMembershipsStrict(authz.FormatTenantMemberships(ms))

	require.NoError(t, err)
	assert.Equal(t, ms, got)
}
