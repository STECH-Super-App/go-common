package auth

import "testing"

func TestResponseForOutcome(t *testing.T) {
	cases := map[string]struct {
		action string
		reason string
	}{
		OutcomeExpired:     {ActionRefresh, "COMMON_TOKEN_EXPIRED"},
		OutcomeStale:       {ActionRefresh, "COMMON_TOKEN_STALE"},
		OutcomeLoggedOut:   {ActionReauth, "COMMON_SESSION_LOGGED_OUT"},
		OutcomeRevoked:     {ActionReauth, "COMMON_SESSION_REVOKED"},
		OutcomeSuspended:   {ActionReauth, "COMMON_ACCOUNT_SUSPENDED"},
		OutcomeUntrusted:   {ActionReauth, "COMMON_AUTH_REQUIRED"},
		OutcomeNotYetValid: {ActionReauth, "COMMON_AUTH_REQUIRED"},
	}
	for outcome, want := range cases {
		got, ok := ResponseForOutcome(outcome)
		if !ok {
			t.Fatalf("%s: expected ok=true", outcome)
		}
		if got.Action != want.action || got.Reason != want.reason {
			t.Errorf("%s: got (%s, %s), want (%s, %s)", outcome, got.Action, got.Reason, want.action, want.reason)
		}
		if got.Message == "" {
			t.Errorf("%s: expected a non-empty English fallback message", outcome)
		}
	}
}

func TestResponseForOutcome_UnknownIsNotOK(t *testing.T) {
	if _, ok := ResponseForOutcome("nonsense"); ok {
		t.Error("unknown outcome must return ok=false")
	}
	if _, ok := ResponseForOutcome(""); ok {
		t.Error("empty outcome must return ok=false")
	}
}

func TestOutcomeForRevokeReason(t *testing.T) {
	cases := map[string]string{
		RevokeReasonUserUpdated:  OutcomeStale,
		RevokeReasonTokenRefresh: OutcomeStale,
		RevokeReasonLogout:       OutcomeLoggedOut,
		RevokeReasonTheft:        OutcomeRevoked,
		RevokeReasonAdminBan:     OutcomeSuspended,
		"some_future_reason":     OutcomeRevoked, // fail-safe default → reauth
	}
	for reason, want := range cases {
		if got := OutcomeForRevokeReason(reason); got != want {
			t.Errorf("reason %q: got %q, want %q", reason, got, want)
		}
	}
}
