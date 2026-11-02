package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	claimsTenantA = "11111111-1111-1111-1111-111111111111"
	claimsTenantB = "22222222-2222-2222-2222-222222222222"
)

// A claim without a start time marshals exactly as it did before `at` existed:
// the key is omitted, so existing tokens and their size are unchanged.
func TestTenantClaimMarshalOmitsUnknownAt(t *testing.T) {
	b, err := json.Marshal(TenantClaim{ID: claimsTenantA, R: "A", Kind: "organisation"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"`+claimsTenantA+`","r":"A","kind":"organisation"}`, string(b))
	assert.NotContains(t, string(b), `"at"`)
}

func TestClaimsMarshalOmitsEmptyReadonlyTenants(t *testing.T) {
	b, err := json.Marshal(Claims{UserID: "u", Tenants: []TenantClaim{{ID: claimsTenantA, R: "A"}}})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "tns_ro")
}

func TestClaimsRoundTripStartTimeAndReadonlyTenants(t *testing.T) {
	in := Claims{
		UserID: "u",
		Tenants: []TenantClaim{
			{ID: claimsTenantA, R: "A", Kind: "organisation", At: 1767225600},
		},
		TenantsReadonly: []TenantClaim{
			{ID: claimsTenantB, R: "M", At: 1767225601},
		},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"at":1767225600`)
	assert.Contains(t, string(b), `"tns_ro":[{"id":"`+claimsTenantB+`","r":"M","at":1767225601}]`)

	var out Claims
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.Tenants, out.Tenants)
	assert.Equal(t, in.TenantsReadonly, out.TenantsReadonly)
}

// A token minted before this change decodes with no start time and no
// read-only list.
func TestClaimsDecodeLegacyTenantsWithoutAt(t *testing.T) {
	var out Claims
	require.NoError(t, json.Unmarshal([]byte(
		`{"sub":"u","tns":[{"id":"`+claimsTenantA+`","r":"A","kind":"organisation"}]}`), &out))
	require.Len(t, out.Tenants, 1)
	assert.Zero(t, out.Tenants[0].At)
	assert.Empty(t, out.TenantsReadonly)
}
