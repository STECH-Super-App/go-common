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

func TestOutcomeForRevokeReason_AccountDeleted(t *testing.T) {
	got := OutcomeForRevokeReason(RevokeReasonAccountDeleted)
	if got == OutcomeRevoked {
		t.Fatalf("account deletion must not map to OutcomeRevoked (that tells the client to log in again, producing a login loop); got %v", got)
	}
	if got != OutcomeAccountDeleted {
		t.Fatalf("OutcomeForRevokeReason(RevokeReasonAccountDeleted) = %v, want OutcomeAccountDeleted", got)
	}
}

// TestResponseForOutcome_AccountDeleted is the other half of the same contract.
// OutcomeForRevokeReason mapping to a value that outcomeResponses does not know
// is worse than useless: AuthMiddleware fails an unrecognized outcome safe to
// (reauth, COMMON_AUTH_REQUIRED), which is precisely the "log in again" loop the
// terminal outcome exists to avoid. The distinguishing signal is the Reason.
func TestResponseForOutcome_AccountDeleted(t *testing.T) {
	got, ok := ResponseForOutcome(OutcomeAccountDeleted)
	if !ok {
		t.Fatal("OutcomeAccountDeleted must be a recognized outcome, otherwise the middleware falls back to the generic auth-required failure")
	}
	if got.Reason == "COMMON_AUTH_REQUIRED" || got.Reason == "COMMON_SESSION_REVOKED" {
		t.Fatalf("a deleted account needs its own Reason so the client can route to onboarding instead of the login screen; got %q", got.Reason)
	}
	if got.Action == ActionRefresh {
		t.Fatal("a deleted account must never be offered a refresh — the refresh would be revoked too, looping the client")
	}
	if got.Message == "" {
		t.Fatal("expected a non-empty English fallback message")
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
