package auth

import (
	"encoding/json"
	"strings"
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

// SL-25: the chat_read claim is gone from Claims. A token minted before it was
// retired may still carry one — valid, malformed or enormous — and must keep
// decoding: the unknown claim is ignored, never a reason to refuse the login of
// someone whose token is still within its lifetime.
func TestClaimsIgnoreRetiredChatReadClaim(t *testing.T) {
	const prefix = `{"sub":"user","roles":["user"],"tns":[{"id":"12345678-1234-1234-1234-123456789abc","r":"ADMIN"}]`
	huge := strings.Repeat(`{"id":"12345678-1234-1234-1234-123456789abc","r":"MANAGER"},`, 2000)

	for name, claim := range map[string]string{
		"absent":              prefix + `}`,
		"null":                prefix + `,"chat_read":null}`,
		"valid":               prefix + `,"chat_read":{"s":"READY","m":[]}}`,
		"unknown field":       prefix + `,"chat_read":{"s":"READY","m":[],"future":1}}`,
		"duplicate key":       prefix + `,"chat_read":{"s":"READY","s":"NOT_READY","m":[]}}`,
		"wrong type":          prefix + `,"chat_read":"nonsense"}`,
		"over the old 64 KiB": prefix + `,"chat_read":{"s":"READY","m":[` + huge + `{}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var c Claims
			if err := json.Unmarshal([]byte(claim), &c); err != nil {
				t.Fatalf("a retired chat_read claim must not fail decoding: %v", err)
			}
			if c.UserID != "user" || len(c.Tenants) != 1 || c.Tenants[0].R != "ADMIN" {
				t.Fatalf("the rest of the claims changed: %+v", c)
			}
		})
	}
}

// A newly minted token no longer has the field to carry one.
func TestClaimsMarshalHasNoChatRead(t *testing.T) {
	b, err := json.Marshal(Claims{UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "chat_read") {
		t.Fatalf("unexpected chat_read in %s", b)
	}
}
