// Package grpcopts holds the house dial options for outbound gRPC clients, so
// every service configures its channels the same way and the reasoning lives in
// one place instead of a dozen copies.
package grpcopts

import (
	"context"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
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

// EnvClientCallTimeout is the environment variable that drives
// DefaultCallTimeoutFromEnv.
const EnvClientCallTimeout = "GRPC_CLIENT_CALL_TIMEOUT"

// DefaultClientCallTimeout is the per-call bound DefaultCallTimeoutFromEnv
// applies when the environment says nothing. It is a ceiling for calls that
// forgot their own deadline, not a target: a call that sets a deadline keeps it.
const DefaultClientCallTimeout = 10 * time.Second

// UnaryTimeoutInterceptor returns a client interceptor that gives a unary call
// a deadline of d when — and only when — its context has none. A caller's own
// deadline, shorter or longer, is never touched, so adopting the default cannot
// shorten a call a service deliberately allowed to run long. A non-positive d
// makes the interceptor a pass-through.
//
// Why: without it a client call whose context carries no deadline waits for as
// long as the server (or the network) does, and unbounded calls keep turning up
// in individual services (SL-30). Streams are left alone — a long-lived stream
// is legitimate.
func UnaryTimeoutInterceptor(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if d <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		if _, has := ctx.Deadline(); has {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// DefaultCallTimeout is the dial option form of UnaryTimeoutInterceptor:
//
//	conn, err := grpc.NewClient(addr, grpcopts.ClientIdleTimeoutFromEnv(), grpcopts.DefaultCallTimeoutFromEnv())
func DefaultCallTimeout(d time.Duration) grpc.DialOption {
	return grpc.WithChainUnaryInterceptor(UnaryTimeoutInterceptor(d))
}

// DefaultCallTimeoutFromEnv reads GRPC_CLIENT_CALL_TIMEOUT and returns the
// matching DefaultCallTimeout option, falling back to DefaultClientCallTimeout
// when the variable is unset, empty or unparseable. An explicit "0s" turns the
// default off. Like ClientIdleTimeoutFromEnv, a typo falls back to the safe
// value rather than failing the boot or silently removing the bound.
func DefaultCallTimeoutFromEnv() grpc.DialOption {
	return DefaultCallTimeout(callTimeoutFromEnv())
}

func callTimeoutFromEnv() time.Duration {
	raw, ok := os.LookupEnv(EnvClientCallTimeout)
	if !ok || raw == "" {
		return DefaultClientCallTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return DefaultClientCallTimeout
	}
	return d
}

// ReasonDialFailed is the Reason of the error NewClient returns.
const ReasonDialFailed = "COMMON_GRPC_DIAL_FAILED"

// NewClient is the fleet's one gRPC client constructor: grpc.NewClient with the
// house defaults already applied — the idle timeout (ClientIdleTimeoutFromEnv)
// and the default per-call deadline (DefaultCallTimeoutFromEnv) — followed by
// the caller's own options, which win where they overlap. It adds no transport
// credentials: pass grpc.WithTransportCredentials as before.
//
// .golangci.strict.yml bans a bare grpc.NewClient / grpc.Dial outside this
// package, so a service cannot dial a channel that waits forever.
//
// A failure is a typed 500 (Critical Rule 7) with the grpc error as its cause.
func NewClient(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	all := make([]grpc.DialOption, 0, len(opts)+2)
	all = append(all, ClientIdleTimeoutFromEnv(), DefaultCallTimeoutFromEnv())
	all = append(all, opts...)

	conn, err := grpc.NewClient(target, all...) //nolint:forbidigo // this IS the helper the guard sends everyone to
	if err != nil {
		return nil, commonErrors.New(http.StatusInternalServerError).
			Reason(ReasonDialFailed).
			Message("failed to create gRPC client for " + target).
			Cause(err).
			Build()
	}
	return conn, nil
}
