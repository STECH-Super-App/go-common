package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

const strictMember = `{"id":"12345678-1234-1234-1234-123456789abc","r":"MANAGER","s":"SUSPENDED","g":"23456789-1234-1234-1234-123456789abc","at":"2026-01-01T00:00:00Z","b":"ACTUAL"}`

func assertChatReadInvalid(t *testing.T, err error) {
	t.Helper()
	var app *commonerr.AppError
	if !errors.As(err, &app) || app.Reason != ReasonChatReadContextInvalid {
		t.Fatalf("want typed %s, got %v", ReasonChatReadContextInvalid, err)
	}
}

func TestClaimsChatReadStrictJSON(t *testing.T) {
	prefix := `{"sub":"user","tns":[{"id":"12345678-1234-1234-1234-123456789abc","r":"ADMIN"}]`
	tests := []struct {
		name    string
		claims  string
		bad     bool
		present bool
		status  string
		members int
	}{
		{"absent", prefix + `}`, false, false, "", 0},
		{"null", prefix + `,"chat_read":null}`, false, false, "", 0},
		{"ready", prefix + `,"chat_read":{"s":"READY","m":[` + strictMember + `]}}`, false, true, ChatReadReady, 1},
		{"ready empty", prefix + `,"chat_read":{"s":"READY","m":[]}}`, false, true, ChatReadReady, 0},
		{"not ready", prefix + `,"chat_read":{"s":"NOT_READY","m":[]}}`, false, true, ChatReadNotReady, 0},
		{"unknown top-level key", prefix + `,"chat_read":{"s":"READY","m":[],"x":1}}`, true, false, "", 0},
		{"duplicate top-level key", prefix + `,"chat_read":{"s":"READY","s":"NOT_READY","m":[]}}`, true, false, "", 0},
		{"unknown member key", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(strictMember, `"b":"ACTUAL"`, `"b":"ACTUAL","x":1`, 1) + `]}}`, true, false, "", 0},
		{"duplicate member key", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(strictMember, `"b":"ACTUAL"`, `"b":"ACTUAL","b":"FEATURE_CUTOVER"`, 1) + `]}}`, true, false, "", 0},
		{"invalid member value", prefix + `,"chat_read":{"s":"READY","m":[` + strings.Replace(strictMember, `MANAGER`, `OWNER`, 1) + `]}}`, true, false, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Claims
			err := json.Unmarshal([]byte(tt.claims), &c)
			if tt.bad {
				assertChatReadInvalid(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Tenants) != 1 || c.Tenants[0].R != ChatRoleAdmin || c.UserID != "user" {
				t.Fatalf("legacy claims changed: %+v", c)
			}
			if (c.ChatRead != nil) != tt.present {
				t.Fatalf("presence = %v, want %v", c.ChatRead != nil, tt.present)
			}
			if tt.present && (c.ChatRead.Status != tt.status || len(c.ChatRead.Memberships) != tt.members) {
				t.Fatalf("context = %+v", c.ChatRead)
			}
		})
	}
}

func TestChatReadTrailingDataRejectedOnBothPaths(t *testing.T) {
	for _, raw := range []string{`{"s":"READY","m":[]} {}`, `{"s":"READY","m":[]}x`} {
		var c ChatReadContext
		assertChatReadInvalid(t, c.UnmarshalJSON([]byte(raw)))
		_, err := ParseChatReadContext(base64.RawURLEncoding.EncodeToString([]byte(raw)))
		assertChatReadInvalid(t, err)
	}
}
