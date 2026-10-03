package lifecycle_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/STECH-Super-App/go-common/pkg/lifecycle"
)

// --- helpers ---

// journal records events in the order they happen, across goroutines.
type journal struct {
	mu     sync.Mutex
	events []string
}

func (j *journal) add(e string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, e)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.events...)
}

func (j *journal) index(e string) int {
	for i, got := range j.list() {
		if got == e {
			return i
		}
	}
	return -1
}

// blockingServer is a Server whose start blocks until stop is called.
func blockingServer(j *journal, name string) (start func() error, stop func(context.Context) error) {
	stopped := make(chan struct{})
	var once sync.Once
	start = func() error {
		<-stopped
		return nil
	}
	stop = func(context.Context) error {
		j.add(name + ":stop")
		once.Do(func() { close(stopped) })
		return nil
	}
	return start, stop
}

// runAsync starts app.Run(ctx) and returns its result channel.
func runAsync(ctx context.Context, app *lifecycle.App) <-chan error {
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	return done
}

func waitResult(t *testing.T, done <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Run did not return within %v", within)
		return nil
	}
}

func observedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return zap.New(core), logs
}

func newEcho() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	return e
}

func waitEchoAddr(t *testing.T, e *echo.Echo) string {
	t.Helper()
	var addr net.Addr
	require.Eventually(t, func() bool {
		addr = e.ListenerAddr()
		return addr != nil
	}, 2*time.Second, 5*time.Millisecond, "echo never started listening")
	return "http://" + addr.String()
}

// --- the Run contract ---

// Phases run in order: servers stop, THEN the workers' context is cancelled,
// THEN closers run, in registration order (not reverse).
func TestRun_PhaseOrder(t *testing.T) {
	j := &journal{}
	app := lifecycle.New(zap.NewNop())

	start, stop := blockingServer(j, "srv")
	app.Server("srv", start, stop)
	app.Worker("w", func(ctx context.Context) error {
		<-ctx.Done()
		j.add("worker:cancelled")
		return ctx.Err()
	})
	for _, name := range []string{"kafka", "redis", "postgres", "tracing"} {
		app.Closer(name, func(context.Context) error {
			j.add("closer:" + name)
			return nil
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	require.NoError(t, waitResult(t, done, 5*time.Second))
	assert.Equal(t, []string{
		"srv:stop",
		"worker:cancelled",
		"closer:kafka",
		"closer:redis",
		"closer:postgres",
		"closer:tracing",
	}, j.list())
}

// The workers' context is NOT the signal context: while a server drains,
// consumers keep running; it is cancelled only after the servers stopped.
// It still carries the Run context's values.
func TestRun_WorkersOutliveServerDrain(t *testing.T) {
	type key struct{}
	app := lifecycle.New(zap.NewNop())

	var workerCtx context.Context
	workerStarted := make(chan struct{})
	app.Worker("consumer", func(ctx context.Context) error {
		workerCtx = ctx
		close(workerStarted)
		<-ctx.Done()
		return ctx.Err()
	})

	var errDuringDrain error
	serverStopped := make(chan struct{})
	app.Server("srv",
		func() error { <-serverStopped; return nil },
		func(context.Context) error {
			time.Sleep(100 * time.Millisecond) // an in-flight request finishing
			errDuringDrain = workerCtx.Err()
			close(serverStopped)
			return nil
		})

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	done := runAsync(ctx, app)
	<-workerStarted
	cancel()

	require.NoError(t, waitResult(t, done, 5*time.Second))
	assert.NoError(t, errDuringDrain, "the worker context must stay live while servers drain")
	assert.Error(t, workerCtx.Err(), "the worker context must be cancelled once servers stopped")
	assert.Equal(t, "v", workerCtx.Value(key{}), "the worker context keeps the Run context's values")
}

// A real Echo server drains an in-flight request on shutdown, and a worker
// sees cancellation only after the response was written.
func TestRun_HTTPDrainsInFlightRequest(t *testing.T) {
	j := &journal{}
	e := newEcho()
	inHandler := make(chan struct{})
	e.GET("/slow", func(c echo.Context) error {
		close(inHandler)
		time.Sleep(200 * time.Millisecond)
		j.add("request:done")
		return c.String(http.StatusOK, "ok")
	})

	app := lifecycle.New(zap.NewNop())
	app.HTTP("http", e, "127.0.0.1:0")
	app.Worker("consumer", func(ctx context.Context) error {
		<-ctx.Done()
		j.add("worker:cancelled")
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	base := waitEchoAddr(t, e)

	type resp struct {
		code int
		body string
		err  error
	}
	got := make(chan resp, 1)
	go func() {
		r, err := http.Get(base + "/slow") //nolint:gosec // test URL from a local listener
		if err != nil {
			got <- resp{err: err}
			return
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		got <- resp{code: r.StatusCode, body: string(b)}
	}()
	<-inHandler
	cancel()

	require.NoError(t, waitResult(t, done, 5*time.Second))
	r := <-got
	require.NoError(t, r.err, "the in-flight request must complete")
	assert.Equal(t, http.StatusOK, r.code)
	assert.Equal(t, "ok", r.body)
	assert.Less(t, j.index("request:done"), j.index("worker:cancelled"))
}

// A graceful HTTP shutdown that overruns the budget is hard-stopped
// (e.Close), reported as ErrShutdownTimeout, and Run still returns promptly.
func TestRun_HTTPBudgetExpiryHardStops(t *testing.T) {
	e := newEcho()
	inHandler := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	e.GET("/stuck", func(c echo.Context) error {
		close(inHandler)
		<-release
		return c.String(http.StatusOK, "late")
	})

	logger, logs := observedLogger()
	app := lifecycle.New(logger, lifecycle.WithShutdownBudget(200*time.Millisecond))
	app.HTTP("http", e, "127.0.0.1:0")
	closerRan := false
	app.Closer("after", func(context.Context) error { closerRan = true; return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	base := waitEchoAddr(t, e)

	clientErr := make(chan error, 1)
	go func() {
		r, err := http.Get(base + "/stuck") //nolint:gosec // test URL from a local listener
		if err == nil {
			_ = r.Body.Close()
		}
		clientErr <- err
	}()
	<-inHandler
	start := time.Now()
	cancel()

	err := waitResult(t, done, 3*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
	assert.Less(t, time.Since(start), time.Second, "the budget must bound Run")
	assert.Error(t, <-clientErr, "the hard stop must close the stuck connection")
	assert.NotEmpty(t, logs.FilterMessage("server did not stop within the shutdown budget; hard stop").All())
	assert.False(t, closerRan, "the budget was spent in phase 1, so the closer is skipped")
	assertShutdownComplete(t, logs, "signal", []string{"server:http", "closer:after"})
}

// gRPC: GracefulStop waits for an open stream forever; on the deadline Stop
// ends it, Run reports the timeout and returns.
func TestRun_GRPCBudgetExpiryHardStops(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())

	app := lifecycle.New(zap.NewNop(), lifecycle.WithShutdownBudget(200*time.Millisecond))
	app.GRPC("grpc", srv, lis)

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	stream, err := healthpb.NewHealthClient(conn).Watch(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = stream.Recv() // the stream is established and held open
	require.NoError(t, err)

	cancel()
	err = waitResult(t, done, 3*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
	_, err = stream.Recv()
	assert.Error(t, err, "the hard stop must end the stream")
}

// A gRPC server with nothing in flight stops cleanly.
func TestRun_GRPCGracefulStop(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())

	app := lifecycle.New(zap.NewNop())
	app.GRPC("grpc", srv, lis)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	cancel()
	require.NoError(t, waitResult(t, done, 5*time.Second))
}

// A stop function that ignores its context cannot hang Run: it is abandoned
// at the deadline and reported.
func TestRun_StopIgnoringContextIsAbandoned(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	app := lifecycle.New(zap.NewNop(), lifecycle.WithShutdownBudget(150*time.Millisecond))
	app.Server("deaf",
		func() error { <-block; return nil },
		func(context.Context) error { <-block; return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	err := waitResult(t, done, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
}

// A server failing to start (port in use) triggers shutdown: the other
// components are still stopped and closed in order, and Run fails.
func TestRun_ServerStartFailureTriggersShutdown(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = occupied.Close() }()

	j := &journal{}
	app := lifecycle.New(zap.NewNop())
	app.HTTP("http", newEcho(), occupied.Addr().String())
	app.Worker("consumer", func(ctx context.Context) error {
		<-ctx.Done()
		j.add("worker:cancelled")
		return ctx.Err()
	})
	app.Closer("postgres", func(context.Context) error { j.add("closer:postgres"); return nil })

	err = waitResult(t, runAsync(context.Background(), app), 5*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrServerExited)
	assert.Equal(t, []string{"worker:cancelled", "closer:postgres"}, j.list())
}

// Any server return before shutdown is a failure — a nil one included.
func TestRun_ServerReturningNilEarlyIsAFailure(t *testing.T) {
	app := lifecycle.New(zap.NewNop())
	app.Server("quitter", func() error { return nil }, func(context.Context) error { return nil })

	err := waitResult(t, runAsync(context.Background(), app), 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrServerExited)
}

// A failing worker triggers shutdown and Run returns its error.
func TestRun_WorkerFailureTriggersShutdown(t *testing.T) {
	boom := errors.New("consumer group wedged")
	j := &journal{}
	app := lifecycle.New(zap.NewNop())
	start, stop := blockingServer(j, "srv")
	app.Server("srv", start, stop)
	app.Worker("bad", func(context.Context) error { return boom })

	err := waitResult(t, runAsync(context.Background(), app), 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrWorkerFailed)
	require.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"srv:stop"}, j.list())
}

// A worker returning nil early is logged and does NOT trigger shutdown.
func TestRun_WorkerReturningNilDoesNotTrigger(t *testing.T) {
	logger, logs := observedLogger()
	app := lifecycle.New(logger)
	app.Worker("one-shot", func(context.Context) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)

	require.Eventually(t, func() bool {
		return logs.FilterMessage("worker returned early without error; continuing").Len() == 1
	}, 2*time.Second, 5*time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run returned on a nil worker exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	require.NoError(t, waitResult(t, done, 2*time.Second))
}

// A panicking worker is recovered, logged with its stack and treated as a
// failure that triggers shutdown.
func TestRun_WorkerPanicIsAFailure(t *testing.T) {
	logger, logs := observedLogger()
	app := lifecycle.New(logger)
	app.Worker("panicky", func(context.Context) error { panic("nil map write") })

	err := waitResult(t, runAsync(context.Background(), app), 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrPanic)
	require.ErrorIs(t, err, lifecycle.ErrWorkerFailed)
	entries := logs.FilterMessage("panic recovered").All()
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].ContextMap(), "stack")
}

// A panicking server start is a failure too.
func TestRun_ServerPanicIsAFailure(t *testing.T) {
	app := lifecycle.New(zap.NewNop())
	app.Server("panicky", func() error { panic("bind") }, func(context.Context) error { return nil })

	err := waitResult(t, runAsync(context.Background(), app), 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrPanic)
	require.ErrorIs(t, err, lifecycle.ErrServerExited)
}

// A closer that fails or panics is reported; the next closer still runs. A
// signal-triggered shutdown with a closer error is NOT clean.
func TestRun_CloserFailuresAreJoinedAndDoNotStopTheChain(t *testing.T) {
	flushErr := errors.New("flush failed")
	j := &journal{}
	app := lifecycle.New(zap.NewNop())
	app.Closer("kafka", func(context.Context) error { j.add("kafka"); return flushErr })
	app.Closer("redis", func(context.Context) error { j.add("redis"); panic("closed twice") })
	app.Closer("postgres", func(context.Context) error { j.add("postgres"); return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	err := waitResult(t, done, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrCloserFailed)
	require.ErrorIs(t, err, flushErr)
	require.ErrorIs(t, err, lifecycle.ErrPanic)
	assert.Equal(t, []string{"kafka", "redis", "postgres"}, j.list())
}

// A worker that ignores cancellation is reported as timed out once the
// budget runs out; the closers that no longer fit are skipped and reported.
func TestRun_WorkerIgnoringCancelTimesOut(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	logger, logs := observedLogger()
	app := lifecycle.New(logger, lifecycle.WithShutdownBudget(150*time.Millisecond))
	app.Worker("deaf", func(context.Context) error { <-block; return nil })
	app.Closer("postgres", func(context.Context) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	err := waitResult(t, done, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
	assertShutdownComplete(t, logs, "signal", []string{"worker:deaf", "closer:postgres"})
}

// A worker that fails while shutting down (anything but its own
// cancellation) makes the shutdown unclean.
func TestRun_WorkerFailingDuringShutdownIsReported(t *testing.T) {
	commitErr := errors.New("commit offsets: broker gone")
	app := lifecycle.New(zap.NewNop())
	app.Worker("consumer", func(ctx context.Context) error {
		<-ctx.Done()
		return commitErr
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	err := waitResult(t, done, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrWorkerFailed)
	require.ErrorIs(t, err, commitErr)
}

// Stopping() closes when shutdown begins — before the drain delay ends and
// before any server stops — and servers stop only after the delay.
func TestRun_StoppingAndDrainDelay(t *testing.T) {
	const delay = 150 * time.Millisecond
	app := lifecycle.New(zap.NewNop(), lifecycle.WithDrainDelay(delay))

	var stopAt time.Time
	stopped := make(chan struct{})
	app.Server("srv",
		func() error { <-stopped; return nil },
		func(context.Context) error {
			stopAt = time.Now()
			close(stopped)
			return nil
		})

	select {
	case <-app.Stopping():
		t.Fatal("Stopping closed before Run")
	default:
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	triggerAt := time.Now()
	cancel()

	select {
	case <-app.Stopping():
	case <-time.After(time.Second):
		t.Fatal("Stopping never closed")
	}
	stoppingAt := time.Now()

	require.NoError(t, waitResult(t, done, 2*time.Second))
	assert.Less(t, stoppingAt.Sub(triggerAt), delay, "Stopping must close before the drain delay ends")
	assert.GreaterOrEqual(t, stopAt.Sub(triggerAt), delay-10*time.Millisecond, "servers stop after the drain delay")
}

// A drain delay longer than the budget is cut by the budget.
func TestRun_DrainDelayBoundedByBudget(t *testing.T) {
	app := lifecycle.New(zap.NewNop(),
		lifecycle.WithDrainDelay(time.Hour),
		lifecycle.WithShutdownBudget(100*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()
	require.NoError(t, waitResult(t, done, 2*time.Second))
}

// Clean signal shutdown: nil, and a "shutdown complete" line naming the
// trigger with no timed-out component.
func TestRun_CleanShutdownLogsCompletion(t *testing.T) {
	logger, logs := observedLogger()
	app := lifecycle.New(logger)
	app.Worker("consumer", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()

	require.NoError(t, waitResult(t, done, 2*time.Second))
	assertShutdownComplete(t, logs, "signal", nil)
}

// A real SIGTERM delivered to this test process cancels SignalContext, and
// Run turns it into a clean nil shutdown.
func TestRun_RealSIGTERMIsACleanShutdown(t *testing.T) {
	ctx, stop := lifecycle.SignalContext(context.Background())
	defer stop()

	logger, logs := observedLogger()
	app := lifecycle.New(logger)
	workerUp := make(chan struct{})
	app.Worker("consumer", func(wctx context.Context) error {
		close(workerUp)
		<-wctx.Done()
		return wctx.Err()
	})
	done := runAsync(ctx, app)
	<-workerUp

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	require.NoError(t, waitResult(t, done, 5*time.Second))
	assertShutdownComplete(t, logs, "signal", nil)
}

// --- programmer errors ---

func TestRun_RegistrationAfterRunPanics(t *testing.T) {
	app := lifecycle.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	require.Eventually(t, func() bool {
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			app.Closer("late", func(context.Context) error { return nil })
		}()
		return panicked
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	require.NoError(t, waitResult(t, done, 2*time.Second))
}

func TestRun_CalledTwicePanics(t *testing.T) {
	app := lifecycle.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, app.Run(ctx))
	assert.Panics(t, func() { _ = app.Run(ctx) })
}

func assertShutdownComplete(t *testing.T, logs *observer.ObservedLogs, trigger string, timedOut []string) {
	t.Helper()
	entries := logs.FilterMessage("shutdown complete").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	assert.Equal(t, trigger, fields["trigger"])
	assert.Contains(t, fields, "duration")
	got, _ := fields["timed_out"].([]interface{})
	gotNames := make([]string, 0, len(got))
	for _, g := range got {
		gotNames = append(gotNames, g.(string))
	}
	if timedOut == nil {
		timedOut = []string{}
	}
	assert.ElementsMatch(t, timedOut, gotNames)
}
