package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	kindServer = "server"
	kindWorker = "worker"
	kindCloser = "closer"

	// triggerSignal labels a shutdown started by the Run context ending —
	// normally SIGINT/SIGTERM via SignalContext.
	triggerSignal = "signal"
)

// exit is what a server's start or a worker's run reports when it returns.
type exit struct {
	kind string
	name string
	err  error
}

// report accumulates the shutdown's failures; phase 1 writes it concurrently.
type report struct {
	mu       sync.Mutex
	errs     []error
	timedOut []string
}

func (r *report) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *report) timeout(kind, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timedOut = append(r.timedOut, kind+":"+name)
	r.errs = append(r.errs, fmt.Errorf("%w: %s %q", ErrShutdownTimeout, kind, name))
}

// Run starts every registered server and worker, blocks until the shutdown
// trigger, then shuts down in phases inside one budget:
//
//  0. Stopping() is closed, then the drain delay (if any) elapses.
//  1. Servers stop in parallel, gracefully; a server still busy at the
//     deadline is hard-stopped (Echo Close / gRPC Stop).
//  2. The workers' context is cancelled — only now — and Run waits for them.
//  3. Closers run sequentially in registration order; the members of a
//     CloserGroup run concurrently within the group's one step.
//
// The trigger is the first of: ctx ending (a signal), any server's start
// function returning, or a worker failing. Run returns nil for a clean,
// signal-triggered shutdown, and otherwise an errors.Join of the triggering
// failure and every timeout, closer error and late worker failure. It never
// exits the process; main maps a non-nil result to os.Exit(1).
//
// Run may be called once. Calling it again, or registering a component after
// it, panics.
func (a *App) Run(ctx context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		panic("lifecycle: Run called twice")
	}
	a.started = true
	a.mu.Unlock()

	// Every start/run goroutine sends exactly one exit, so this buffer means
	// none of them ever blocks — including the ones still running after Run
	// has returned on a timeout.
	exits := make(chan exit, len(a.servers)+len(a.workers))

	// The workers' context is detached from ctx's cancellation (values are
	// kept): a signal must not stop the consumers while requests still drain.
	workerCtx, cancelWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWorkers()

	a.startComponents(workerCtx, exits)

	trigger, triggerErr := a.awaitTrigger(ctx, exits)
	began := time.Now()
	close(a.stopping)
	a.log.Info("shutdown started",
		zap.String("trigger", trigger),
		zap.Duration("budget", a.budget),
		zap.Error(triggerErr),
	)

	shutdownCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), began.Add(a.budget))
	defer cancel()

	rep := &report{}
	a.drain(shutdownCtx)
	a.stopServers(shutdownCtx, rep)
	cancelWorkers()
	a.waitWorkers(shutdownCtx, rep)
	a.runClosers(shutdownCtx, rep)
	a.collectLateExits(exits, rep)

	result := errors.Join(append([]error{triggerErr}, rep.errs...)...)
	fields := []zap.Field{
		zap.String("trigger", trigger),
		zap.Duration("duration", time.Since(began)),
		zap.Strings("timed_out", rep.timedOut),
	}
	if result != nil {
		a.log.Error("shutdown complete", append(fields, zap.Error(result))...)
	} else {
		a.log.Info("shutdown complete", fields...)
	}
	return result
}

// startComponents launches servers then workers, in registration order, each
// in its own goroutine with panic recovery.
func (a *App) startComponents(workerCtx context.Context, exits chan<- exit) {
	for _, s := range a.servers {
		go func() {
			defer close(s.done)
			err := a.safeCall(kindServer, s.name, s.start)
			exits <- exit{kind: kindServer, name: s.name, err: err}
		}()
		a.log.Info("server starting", zap.String("server", s.name))
	}
	for _, w := range a.workers {
		go func() {
			defer close(w.done)
			err := a.safeCall(kindWorker, w.name, func() error { return w.run(workerCtx) })
			exits <- exit{kind: kindWorker, name: w.name, err: err}
		}()
		a.log.Info("worker starting", zap.String("worker", w.name))
	}
}

// awaitTrigger blocks until shutdown must begin and says why. The error is
// nil only for a ctx-triggered (signal) shutdown.
func (a *App) awaitTrigger(ctx context.Context, exits <-chan exit) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return triggerSignal, nil
		case ev := <-exits:
			if ev.kind == kindServer {
				cause := ev.err
				if cause == nil {
					cause = errors.New("start returned nil")
				}
				return kindServer + ":" + ev.name,
					fmt.Errorf("%w: %q: %w", ErrServerExited, ev.name, cause)
			}
			if ev.err == nil {
				a.log.Warn("worker returned early without error; continuing",
					zap.String("worker", ev.name))
				continue
			}
			// The worker context cannot be cancelled before shutdown, so any
			// error here — context.Canceled included — is a real failure.
			return kindWorker + ":" + ev.name,
				fmt.Errorf("%w: %q: %w", ErrWorkerFailed, ev.name, ev.err)
		}
	}
}

// drain is phase 0: wait the configured drain delay, bounded by the budget.
func (a *App) drain(ctx context.Context) {
	if a.drainDelay <= 0 {
		return
	}
	t := time.NewTimer(a.drainDelay)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// stopServers is phase 1: every server stops in parallel.
func (a *App) stopServers(ctx context.Context, rep *report) {
	var wg sync.WaitGroup
	for _, s := range a.servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.stopServer(ctx, s, rep)
		}()
	}
	wg.Wait()
}

func (a *App) stopServer(ctx context.Context, s *server, rep *report) {
	overran, err := a.callBounded(ctx, kindServer, s.name+" stop", s.stop)
	if !overran && err != nil && !errors.Is(err, context.DeadlineExceeded) {
		rep.fail(fmt.Errorf("lifecycle: server %q stop: %w", s.name, err))
	}
	overran = overran || errors.Is(err, context.DeadlineExceeded)

	// A graceful stop returning does not mean the serve loop has returned
	// (gRPC Serve, a custom Server); wait for it inside the same budget.
	if !overran {
		select {
		case <-s.done:
		case <-ctx.Done():
			overran = true
		}
	}
	if !overran {
		a.log.Info("server stopped", zap.String("server", s.name))
		return
	}

	rep.timeout(kindServer, s.name)
	if s.hardStop == nil {
		a.log.Warn("server did not stop within the shutdown budget; abandoning it",
			zap.String("server", s.name))
		return
	}
	a.log.Warn("server did not stop within the shutdown budget; hard stop",
		zap.String("server", s.name))
	if herr := a.safeCall(kindServer, s.name+" hard stop", s.hardStop); herr != nil {
		a.log.Warn("server hard stop failed", zap.String("server", s.name), zap.Error(herr))
	}
}

// waitWorkers is phase 2's wait: the worker context is already cancelled.
func (a *App) waitWorkers(ctx context.Context, rep *report) {
	for _, w := range a.workers {
		select {
		case <-w.done:
			continue
		case <-ctx.Done():
		}
		// Budget spent: report every worker still running, then move on.
		for _, straggler := range a.workers {
			select {
			case <-straggler.done:
			default:
				a.log.Warn("worker did not stop within the shutdown budget",
					zap.String("worker", straggler.name))
				rep.timeout(kindWorker, straggler.name)
			}
		}
		return
	}
	a.log.Info("workers stopped", zap.Int("count", len(a.workers)))
}

// runClosers is phase 3: sequential, registration order, remaining budget. A
// CloserGroup is one step of that order whose members run concurrently.
func (a *App) runClosers(ctx context.Context, rep *report) {
	for _, c := range a.closers {
		if !c.group {
			a.runCloser(ctx, c.name, c.fn, rep)
			continue
		}
		var wg sync.WaitGroup
		for _, m := range c.members {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.runCloser(ctx, c.name+"/"+m.Name, m.Close, rep)
			}()
		}
		// Every member's wait is bounded by ctx, so this returns at the latest
		// when the budget ends; a member still running then is abandoned.
		wg.Wait()
	}
}

// runCloser runs one closer (or one group member) within the remaining budget
// and records its outcome.
func (a *App) runCloser(ctx context.Context, name string, fn func(context.Context) error, rep *report) {
	if ctx.Err() != nil {
		// Starting a closer we cannot wait for would run it concurrently
		// with the next one and break the written order; skip it instead.
		a.log.Warn("closer skipped: shutdown budget already spent",
			zap.String("closer", name))
		rep.timeout(kindCloser, name)
		return
	}
	overran, err := a.callBounded(ctx, kindCloser, name, fn)
	switch {
	case overran || (ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded)):
		a.log.Warn("closer did not finish within the shutdown budget",
			zap.String("closer", name))
		rep.timeout(kindCloser, name)
	case err != nil:
		a.log.Error("closer failed", zap.String("closer", name), zap.Error(err))
		rep.fail(fmt.Errorf("%w: %q: %w", ErrCloserFailed, name, err))
	default:
		a.log.Info("closer done", zap.String("closer", name))
	}
}

// collectLateExits reads the exits that arrived after the trigger. A worker
// failing during shutdown (anything but its own cancellation) and a panic
// anywhere are failures; a server's serve loop returning is expected now.
func (a *App) collectLateExits(exits <-chan exit, rep *report) {
	for {
		select {
		case ev := <-exits:
			a.judgeLateExit(ev, rep)
		default:
			return
		}
	}
}

func (a *App) judgeLateExit(ev exit, rep *report) {
	if ev.err == nil {
		return
	}
	if ev.kind == kindWorker {
		if errors.Is(ev.err, context.Canceled) {
			return // its own cancellation: the context was cancelled in phase 2
		}
		rep.fail(fmt.Errorf("%w: %q: %w", ErrWorkerFailed, ev.name, ev.err))
		return
	}
	if errors.Is(ev.err, ErrPanic) {
		rep.fail(fmt.Errorf("lifecycle: server %q: %w", ev.name, ev.err))
		return
	}
	if !errors.Is(ev.err, http.ErrServerClosed) && !errors.Is(ev.err, grpc.ErrServerStopped) {
		a.log.Warn("server returned an error while stopping",
			zap.String("server", ev.name), zap.Error(ev.err))
	}
}

// callBounded runs fn(ctx) in its own goroutine (with panic recovery) and
// waits for it at most until ctx ends, so a stop or closer that ignores its
// context cannot hang Run. overran reports that the wait was abandoned.
func (a *App) callBounded(ctx context.Context, kind, name string, fn func(context.Context) error) (overran bool, err error) {
	result := make(chan error, 1)
	go func() {
		result <- a.safeCall(kind, name, func() error { return fn(ctx) })
	}()
	select {
	case err := <-result:
		return false, err
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// safeCall runs fn, converting a panic into an ErrPanic failure logged with
// its stack. Nothing a component does may take the process down from here.
func (a *App) safeCall(kind, name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("panic recovered",
				zap.String("kind", kind),
				zap.String("name", name),
				zap.Any("panic", r),
				zap.ByteString("stack", debug.Stack()),
			)
			err = fmt.Errorf("%w: %s %q: %v", ErrPanic, kind, name, r)
		}
	}()
	return fn()
}
