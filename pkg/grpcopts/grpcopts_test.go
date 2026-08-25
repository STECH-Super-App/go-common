package grpcopts

import (
	"testing"
	"time"
)

// TestClientIdleTimeoutFromEnv pins the parsing rules, because every one of them
// is a decision about what happens to a production channel.
func TestClientIdleTimeoutFromEnv(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		env  string
		want time.Duration
	}{
		// Unset must equal grpc-go's own default, so a service adopting this
		// package changes nothing until its environment opts in.
		{"unset matches grpc-go's default", false, "", DefaultClientIdleTimeout},
		{"empty is treated as unset", true, "", DefaultClientIdleTimeout},
		{"zero disables idling", true, "0", 0},
		{"zero seconds disables idling", true, "0s", 0},
		{"an explicit duration is honoured", true, "90s", 90 * time.Second},
		// A typo must not take the service down at boot, and must not silently
		// DISABLE idling either — falling back to 0 would quietly change channel
		// behaviour fleet-wide on a misspelling.
		{"garbage falls back to the default", true, "30", DefaultClientIdleTimeout},
		{"words fall back to the default", true, "never", DefaultClientIdleTimeout},
		// Negative is meaningless and grpc-go would reject it; treat as a typo.
		{"negative falls back to the default", true, "-5m", DefaultClientIdleTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvClientIdleTimeout, tc.env)
			}
			if got := clientIdleTimeoutFromEnv(); got != tc.want {
				t.Fatalf("clientIdleTimeoutFromEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClientIdleTimeoutReturnsAnOption is a smoke test: the option must be
// constructible for the values callers actually pass, including 0.
func TestClientIdleTimeoutReturnsAnOption(t *testing.T) {
	for _, d := range []time.Duration{0, time.Minute, DefaultClientIdleTimeout} {
		if opt := ClientIdleTimeout(d); opt == nil {
			t.Fatalf("ClientIdleTimeout(%v) returned nil", d)
		}
	}
	if opt := ClientIdleTimeoutFromEnv(); opt == nil {
		t.Fatal("ClientIdleTimeoutFromEnv() returned nil")
	}
}
