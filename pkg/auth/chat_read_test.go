package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"testing"
	"time"
)

func validChatContext() *ChatReadContext {
	return &ChatReadContext{Status: ChatReadReady, Memberships: []ChatReadMembership{{TenantID: "12345678-1234-1234-1234-123456789abc", Role: "MANAGER", TenantStatus: "SUSPENDED", GrantID: "23456789-1234-1234-1234-123456789abc", GrantEffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC), GrantBasis: ChatGrantActual}}}
}

func TestChatTokenTransportLimit(t *testing.T) {
	limit := MaxAuthorizationHeaderBytes - len("Authorization: Bearer ") - 2
	if err := ValidateChatAccessTokenSize(strings.Repeat("x", limit)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChatAccessTokenSize(strings.Repeat("x", limit+1)); err == nil {
		t.Fatal("oversized authorization header accepted")
	}
	if err := ValidateChatAccessTokenSize(""); err == nil {
		t.Fatal("empty token accepted")
	}
	claims := Claims{UserID: "person", Name: strings.Repeat("x", MaxAuthorizationHeaderBytes), ChatRead: validChatContext()}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-only-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChatAccessTokenSize(token); err == nil {
		t.Fatal("oversized signed JWT accepted")
	}
}

func TestChatReadContextTransportBoundary(t *testing.T) {
	c := &ChatReadContext{Status: ChatReadReady}
	for i := 1; i <= MaxChatReadMemberships; i++ {
		m := validChatContext().Memberships[0]
		m.TenantID = fmt.Sprintf("%08x-1234-1234-1234-123456789abc", i)
		c.Memberships = append(c.Memberships, m)
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if base64.RawURLEncoding.EncodedLen(len(b)) > MaxChatReadContextBytes {
			if i <= 41 {
				t.Fatalf("transport budget unexpectedly rejects %d memberships", i)
			}
			if _, err := FormatChatReadContext(c); err == nil {
				t.Fatal("oversized context accepted")
			}
			c.Memberships = c.Memberships[:len(c.Memberships)-1]
			encoded, err := FormatChatReadContext(c)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := ParseChatReadContext(encoded)
			if err != nil || len(decoded.Memberships) != len(c.Memberships) {
				t.Fatalf("boundary round trip lost entries: %v", err)
			}
			if _, err := ParseChatReadContext(strings.Repeat("x", MaxChatReadContextBytes+1)); err == nil {
				t.Fatal("oversized header accepted")
			}
			return
		}
	}
	t.Fatal("byte limit did not bind before decoder entry bound")
}

func TestActingTenantSelector(t *testing.T) {
	got, err := ParseActingTenantID("12345678-1234-1234-1234-123456789ABC")
	if err != nil || got != "12345678-1234-1234-1234-123456789abc" {
		t.Fatalf("selector %q: %v", got, err)
	}
	for _, s := range []string{"", "00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if _, err := ParseActingTenantID(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestChatReadContextCodecAndActor(t *testing.T) {
	c := validChatContext()
	raw, err := FormatChatReadContext(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseChatReadContext(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.Membership(c.Memberships[0].TenantID)
	if !ok || m.TenantStatus != "SUSPENDED" || !m.GrantEffectiveAt.Equal(c.Memberships[0].GrantEffectiveAt) {
		t.Fatalf("wrong grant: %+v", m)
	}
	if _, ok := decoded.Membership("33456789-1234-1234-1234-123456789abc"); ok {
		t.Fatal("other actor authorized")
	}
	empty, err := FormatChatReadContext(&ChatReadContext{Status: ChatReadReady})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseChatReadContext(empty)
	if err != nil || parsed.Status != ChatReadReady || len(parsed.Memberships) != 0 {
		t.Fatalf("ready empty: %+v %v", parsed, err)
	}
	unavailable, err := FormatChatReadContext(&ChatReadContext{Status: ChatReadNotReady})
	if err != nil {
		t.Fatal(err)
	}
	unready, err := ParseChatReadContext(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unready.Membership(c.Memberships[0].TenantID); ok {
		t.Fatal("not-ready grants access")
	}
	if _, err := FormatChatReadContext(&ChatReadContext{Status: ChatReadReady, Memberships: make([]ChatReadMembership, MaxChatReadMemberships+1)}); err == nil {
		t.Fatal("oversize set accepted")
	}
}

func TestChatReadContextRejectsIncomplete(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ChatReadContext)
	}{
		{"not ready", func(c *ChatReadContext) { c.Status = ChatReadNotReady }},
		{"unknown status", func(c *ChatReadContext) { c.Status = "FUTURE" }},
		{"bad role", func(c *ChatReadContext) { c.Memberships[0].Role = "OWNER" }},
		{"bad tenant", func(c *ChatReadContext) { c.Memberships[0].TenantID = "wrong" }},
		{"nil grant", func(c *ChatReadContext) { c.Memberships[0].GrantID = "00000000-0000-0000-0000-000000000000" }},
		{"bad timestamp", func(c *ChatReadContext) { c.Memberships[0].GrantEffectiveAt = time.Time{} }},
		{"unknown basis", func(c *ChatReadContext) { c.Memberships[0].GrantBasis = "UNKNOWN" }},
		{"duplicate", func(c *ChatReadContext) { c.Memberships = append(c.Memberships, c.Memberships[0]) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validChatContext()
			tt.mutate(c)
			if _, err := FormatChatReadContext(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, raw := range []string{"", base64.RawURLEncoding.EncodeToString([]byte(`{}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"s":"READY","m":[],"unknown":true}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"s":"READY","s":"READY","m":[]}`)), strings.Repeat("x", MaxChatReadContextBytes+1)} {
		if _, err := ParseChatReadContext(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestJWTChatReadContextStrictDecoding(t *testing.T) {
	const member = `{"id":"12345678-1234-1234-1234-123456789abc","r":"MANAGER","s":"SUSPENDED","g":"23456789-1234-1234-1234-123456789abc","at":"2026-01-01T00:00:00Z","b":"ACTUAL"}`
	const prefix = `{"sub":"user","tns":[{"id":"12345678-1234-1234-1234-123456789abc","r":"ADMIN"}],"legacy_extra":"ignored"`
	cases := []struct {
		name, claim  string
		bad, present bool
	}{
		{"legacy absent", prefix + `}`, false, false},
		{"legacy null", prefix + `,"chat_read":null}`, false, false},
		{"valid", prefix + `,"chat_read":{"s":"READY","m":[` + member + `]}}`, false, true},
		{"ready empty", prefix + `,"chat_read":{"s":"READY","m":[]}}`, false, true},
		{"unknown context", prefix + `,"chat_read":{"s":"READY","m":[],"future":1}}`, true, false},
		{"duplicate context", prefix + `,"chat_read":{"s":"READY","s":"READY","m":[]}}`, true, false},
		{"case alias context", prefix + `,"chat_read":{"s":"READY","S":"READY","m":[]}}`, true, false},
		{"unicode alias context", prefix + `,"chat_read":{"s":"READY","ſ":"READY","m":[]}}`, true, false},
		{"unknown member", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(member, `"r":"MANAGER"`, `"r":"MANAGER","future":true`, 1) + `]}}`, true, false},
		{"duplicate member", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(member, `"r":"MANAGER"`, `"r":"MANAGER","r":"ADMIN"`, 1) + `]}}`, true, false},
		{"case alias member", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(member, `"r":"MANAGER"`, `"r":"MANAGER","R":"ADMIN"`, 1) + `]}}`, true, false},
		{"invalid member", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(member, `"r":"MANAGER"`, `"r":"OWNER"`, 1) + `]}}`, true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			key := []byte("test-only-secret")
			unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(tt.claim))
			sig, err := jwt.SigningMethodHS256.Sign(unsigned, key)
			if err != nil {
				t.Fatal(err)
			}
			signed := unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
			claims := &Claims{}
			token, err := jwt.ParseWithClaims(signed, claims, func(*jwt.Token) (any, error) { return key, nil })
			if tt.bad {
				if err == nil {
					t.Fatalf("malformed context accepted: %+v", claims.ChatRead)
				}
				_, raw, ok := strings.Cut(tt.claim, `"chat_read":`)
				if !ok {
					t.Fatal("missing test context")
				}
				raw = strings.TrimSuffix(raw, "}")
				if _, err := ParseChatReadContext(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
					t.Fatal("forwarded header accepted malformed context")
				}
				return
			}
			if err != nil || token == nil || !token.Valid {
				t.Fatalf("legacy or valid token rejected: %v", err)
			}
			if (claims.ChatRead != nil) != tt.present || len(claims.Tenants) != 1 || claims.Tenants[0].R != "ADMIN" {
				t.Fatalf("legacy claims or context changed: %+v", claims)
			}
		})
	}
}

func TestChatReadContextUnmarshalIsAtomic(t *testing.T) {
	c := validChatContext()
	if err := json.Unmarshal([]byte(`{"s":"READY","m":[],"unexpected":true}`), c); err == nil {
		t.Fatal("unexpected field accepted")
	}
	if len(c.Memberships) != 1 || c.Status != ChatReadReady {
		t.Fatalf("failed decode changed prior context: %+v", c)
	}
	if err := json.Unmarshal([]byte(`null`), c); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Membership("12345678-1234-1234-1234-123456789abc"); ok {
		t.Fatal("null context grants access")
	}
}

func TestClaimsLegacyAndChatReadRoundTrip(t *testing.T) {
	legacy := `{"sub":"user","tns":[{"id":"12345678-1234-1234-1234-123456789abc","r":"ADMIN"}]}`
	var c Claims
	if err := json.Unmarshal([]byte(legacy), &c); err != nil {
		t.Fatal(err)
	}
	if c.ChatRead != nil || len(c.Tenants) != 1 {
		t.Fatalf("legacy changed: %+v", c)
	}
	c.ChatRead = validChatContext()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var out Claims
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tenants) != 1 || out.Tenants[0].R != "ADMIN" || out.ChatRead.Memberships[0].TenantStatus != "SUSPENDED" {
		t.Fatalf("round trip: %+v", out)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte("test-only-secret"))
	if err != nil {
		t.Fatal(err)
	}
	parsed := &Claims{}
	token, err := jwt.ParseWithClaims(signed, parsed, func(*jwt.Token) (any, error) { return []byte("test-only-secret"), nil })
	if err != nil || !token.Valid || parsed.ChatRead == nil || len(parsed.Tenants) != 1 {
		t.Fatalf("signed round trip: %v %+v", err, parsed)
	}
}
