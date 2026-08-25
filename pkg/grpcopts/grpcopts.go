// Package grpcopts holds the house dial options for outbound gRPC clients, so
// every service configures its channels the same way and the reasoning lives in
// one place instead of a dozen copies.
package grpcopts

import (
	"os"
	"time"

	"google.golang.org/grpc"
)

// EnvClientIdleTimeout is the environment variable that drives ClientIdleTimeoutFromEnv.
const EnvClientIdleTimeout = "GRPC_CLIENT_IDLE_TIMEOUT"

// DefaultClientIdleTimeout mirrors grpc-go's own default (dialoptions.go:
// `idleTimeout: 30 * time.Minute`). It is repeated here so an unset environment
// behaves exactly as it did before this package existed — adopting grpcopts is
// a no-op until an environment opts in.
const DefaultClientIdleTimeout = 30 * time.Minute

// ClientIdleTimeout configures how long a channel may sit without RPCs before
// grpc-go tears it down. Pass 0 to disable idling entirely.
//
// # Why this is a house standard rather than a per-service detail
//
// After the idle timeout a channel enters idle mode and shuts down its
// transport, name resolver and load balancer. Nothing is broken by that on its
// own. The cost lands on the NEXT RPC, which must rebuild all of it —
// re-resolve DNS, reopen TCP, redo the HTTP/2 handshake — and it does so INSIDE
// the caller's per-call deadline. Ours are typically 3 seconds.
//
// On a busy path the channel never idles and none of this is observable. On a
// rare path it is the normal case, and the first caller after a quiet spell
// pays the whole reconnection out of their deadline. That is not hypothetical:
// organisation-service's admin-transfer initiate returned DeadlineExceeded at
// exactly 3.000s on a service whose p50 was 4ms, on the first attempt after a
// 565-minute gap in traffic — 19x the idle timeout. The failure looked like a
// dependency outage and was not one.
//
// The paths most likely to break are therefore the ones exercised least, which
// is also where the failure is hardest to reproduce and easiest to misdiagnose.
//
// # Why there is deliberately no keepalive here
//
// The instinct is to pair this with keepalive pings. Do not, without changing
// the servers first. gRPC servers default to EnforcementPolicy.MinTime of 5
// minutes with PermitWithoutStream false, and no server in this fleet overrides
// it. A client pinging an idle connection more often than that is answered with
// GOAWAY "too_many_pings" and the connection is closed — destroying precisely
// the idle connections keepalive was meant to preserve. Keepalive needs a
// server-side EnforcementPolicy to exist first; until then, holding the channel
// open is the whole of the fix.
func ClientIdleTimeout(d time.Duration) grpc.DialOption {
	return grpc.WithIdleTimeout(d)
}

// ClientIdleTimeoutFromEnv reads GRPC_CLIENT_IDLE_TIMEOUT and returns the
// matching dial option, falling back to DefaultClientIdleTimeout when the
// variable is unset or unparseable.
//
// An unparseable value falls back rather than failing: a typo must not take a
// service down at boot. It also must not silently disable idling — the fallback
// is the conservative default, not 0. The trade-off is that the fallback is
// silent, so a misspelled duration looks like the knob is simply not working.
// Services that prefer to surface it should parse it in their own config layer
// and pass ClientIdleTimeout explicitly.
func ClientIdleTimeoutFromEnv() grpc.DialOption {
	return ClientIdleTimeout(clientIdleTimeoutFromEnv())
}

func clientIdleTimeoutFromEnv() time.Duration {
	raw, ok := os.LookupEnv(EnvClientIdleTimeout)
	if !ok || raw == "" {
		return DefaultClientIdleTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return DefaultClientIdleTimeout
	}
	return d
}
