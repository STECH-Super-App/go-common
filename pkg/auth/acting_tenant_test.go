package auth

import "testing"

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
