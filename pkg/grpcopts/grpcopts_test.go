package grpcopts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
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

// SL-30: the house dial options covered only the idle timeout; with no default
// per-call deadline a client call without its own context deadline waits as long
// as the server does — which is why unbounded calls keep appearing per service.
func TestUnaryTimeoutInterceptor_AppliesOnlyWhenThereIsNoDeadline(t *testing.T) {
	var gotDeadline time.Time
	var hadDeadline bool
	var ctxAfter context.Context
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		gotDeadline, hadDeadline = ctx.Deadline()
		ctxAfter = ctx
		return nil
	}
	ic := UnaryTimeoutInterceptor(2 * time.Second)

	// No deadline: the default is applied.
	before := time.Now()
	require.NoError(t, ic(context.Background(), "/svc/M", nil, nil, nil, invoker))
	require.True(t, hadDeadline, "a call with no deadline must get the default")
	assert.WithinDuration(t, before.Add(2*time.Second), gotDeadline, 500*time.Millisecond)
	assert.Error(t, ctxAfter.Err(), "the timer is released when the call returns")

	// The caller's own deadline wins, shorter...
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	want, _ := short.Deadline()
	require.NoError(t, ic(short, "/svc/M", nil, nil, nil, invoker))
	assert.Equal(t, want, gotDeadline)

	// ...and longer: a default never SHORTENS a deadline the caller chose.
	long, cancelLong := context.WithTimeout(context.Background(), time.Hour)
	defer cancelLong()
	want, _ = long.Deadline()
	require.NoError(t, ic(long, "/svc/M", nil, nil, nil, invoker))
	assert.Equal(t, want, gotDeadline)
}

func TestUnaryTimeoutInterceptor_NonPositiveIsPassThrough(t *testing.T) {
	var hadDeadline bool
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		_, hadDeadline = ctx.Deadline()
		return nil
	}
	for _, d := range []time.Duration{0, -time.Second} {
		require.NoError(t, UnaryTimeoutInterceptor(d)(context.Background(), "/svc/M", nil, nil, nil, invoker))
		assert.False(t, hadDeadline)
	}
}

func TestUnaryTimeoutInterceptor_ReturnsTheInvokerError(t *testing.T) {
	boom := errors.New("boom")
	err := UnaryTimeoutInterceptor(time.Second)(context.Background(), "/svc/M", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error { return boom })
	assert.Same(t, boom, err)
}

func TestCallTimeoutFromEnv(t *testing.T) {
	assert.Equal(t, DefaultClientCallTimeout, callTimeoutFromEnv())

	t.Setenv(EnvClientCallTimeout, "3s")
	assert.Equal(t, 3*time.Second, callTimeoutFromEnv())

	t.Setenv(EnvClientCallTimeout, "garbage")
	assert.Equal(t, DefaultClientCallTimeout, callTimeoutFromEnv(), "a typo falls back, never disables the bound")

	t.Setenv(EnvClientCallTimeout, "0s")
	assert.Equal(t, time.Duration(0), callTimeoutFromEnv(), "an explicit 0 turns the default off")
}

func TestDefaultCallTimeout_IsADialOption(t *testing.T) {
	opts := []grpc.DialOption{DefaultCallTimeout(time.Second), DefaultCallTimeoutFromEnv()}
	assert.Len(t, opts, 2)
}

func TestNewClient_BuildsAChannelWithTheHouseDefaults(t *testing.T) {
	var deadline time.Time
	var had bool
	spy := grpc.WithChainUnaryInterceptor(func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn,
		_ grpc.UnaryInvoker, _ ...grpc.CallOption) error {
		deadline, had = ctx.Deadline()
		return nil
	})

	conn, err := NewClient("passthrough:///svc", grpc.WithTransportCredentials(insecure.NewCredentials()), spy)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// The default interceptor was added BEFORE the caller's, so by the time the
	// caller's spy runs the context already carries the default deadline.
	require.NoError(t, conn.Invoke(context.Background(), "/svc/M", nil, nil))
	require.True(t, had, "a call with no deadline reaches the caller's interceptor with the default one")
	assert.WithinDuration(t, time.Now().Add(DefaultClientCallTimeout), deadline, 2*time.Second)
}

func TestNewClient_FailureIsTyped(t *testing.T) {
	_, err := NewClient("passthrough:///svc") // no transport credentials

	require.Error(t, err)
	var ae *commonErrors.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, ReasonDialFailed, ae.Reason)
	assert.NotNil(t, ae.Unwrap())
}
