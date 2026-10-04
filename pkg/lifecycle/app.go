package lifecycle

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// DefaultShutdownBudget is the total time Run allows for every shutdown phase
// together. It fits the Kubernetes default 30 s termination grace period with
// headroom for the kubelet's preStop accounting; a service that adds a preStop
// sleep raises terminationGracePeriodSeconds rather than shrinking this.
const DefaultShutdownBudget = 25 * time.Second

// Option configures an App.
type Option func(*App)

// WithShutdownBudget sets the total shutdown budget — one deadline, measured
// from the shutdown trigger, shared by every phase. Non-positive values are
// ignored (the default stays).
func WithShutdownBudget(d time.Duration) Option {
	return func(a *App) {
		if d > 0 {
			a.budget = d
		}
	}
}

// WithDrainDelay sets a sleep between the shutdown trigger and the server
// stop phase (default 0). Stopping() is already closed during it, so the
// process can fail readiness while the load balancer is still routing to it.
// The delay counts against the shutdown budget. Negative values are ignored.
func WithDrainDelay(d time.Duration) Option {
	return func(a *App) {
		if d >= 0 {
			a.drainDelay = d
		}
	}
}

// server is anything with a blocking start and a context-bounded graceful
// stop. hardStop (optional) runs when the graceful stop overran the budget.
type server struct {
	name     string
	start    func() error
	stop     func(context.Context) error
	hardStop func() error
	done     chan struct{} // closed when start returns
}

type worker struct {
	name string
	run  func(context.Context) error
	done chan struct{} // closed when run returns
}

// closer is one step of phase 3: either a single fn or, for a CloserGroup,
// members that run concurrently within the step.
type closer struct {
	name    string
	fn      func(context.Context) error
	group   bool
	members []NamedCloser
}

// NamedCloser is one member of a CloserGroup.
type NamedCloser struct {
	Name  string
	Close func(context.Context) error
}

// App orchestrates a process's run and shutdown. It accepts already-built
// components; it builds nothing, reads no configuration and never calls
// os.Exit — main keeps ownership of wiring and of the exit code.
//
// Register every component, then call Run exactly once.
type App struct {
	log        *zap.Logger
	budget     time.Duration
	drainDelay time.Duration

	mu      sync.Mutex
	started bool
	servers []*server
	workers []*worker
	closers []*closer

	stopping chan struct{}
}

// New returns an App logging to log (a nil logger means zap.NewNop()).
func New(log *zap.Logger, opts ...Option) *App {
	if log == nil {
		log = zap.NewNop()
	}
	a := &App{
		log:      log,
		budget:   DefaultShutdownBudget,
		stopping: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// HTTP registers an Echo server: e.Start(addr) at run time, e.Shutdown on
// shutdown, and e.Close if the graceful shutdown overruns the budget.
func (a *App) HTTP(name string, e *echo.Echo, addr string) {
	a.addServer(&server{
		name:     name,
		start:    func() error { return e.Start(addr) },
		stop:     e.Shutdown,
		hardStop: e.Close,
	})
}

// GRPC registers a gRPC server: s.Serve(lis) at run time, s.GracefulStop on
// shutdown, and s.Stop if the graceful stop overruns the budget.
func (a *App) GRPC(name string, s *grpc.Server, lis net.Listener) {
	a.addServer(&server{
		name:  name,
		start: func() error { return s.Serve(lis) },
		stop: func(ctx context.Context) error {
			stopped := make(chan struct{})
			go func() {
				s.GracefulStop()
				close(stopped)
			}()
			select {
			case <-stopped:
				return nil
			case <-ctx.Done():
				// s.Stop (the hard stop) also ends the pending GracefulStop.
				return ctx.Err()
			}
		},
		hardStop: func() error {
			s.Stop()
			return nil
		},
	})
}

// Server registers any other server: start blocks while it serves, stop must
// end it gracefully and honour its context's deadline. There is no hard stop —
// a stop that overruns is reported as a timeout and abandoned.
func (a *App) Server(name string, start func() error, stop func(context.Context) error) {
	a.addServer(&server{name: name, start: start, stop: stop})
}

// Worker registers a background loop (a Kafka dispatcher, an outbox, a
// sweeper). run receives a context that is NOT the signal context: it is
// cancelled only after every server has drained, so consumers and the outbox
// relay keep working while in-flight requests finish.
//
// A worker returning a non-nil error before shutdown triggers shutdown. A
// worker returning nil early is logged and does not.
func (a *App) Worker(name string, run func(ctx context.Context) error) {
	a.register(func() {
		a.workers = append(a.workers, &worker{name: name, run: run, done: make(chan struct{})})
	})
}

// Closer registers a teardown step. Closers run sequentially in REGISTRATION
// order after every worker has stopped, each with the remaining budget.
// Recommended order: Kafka readers/writers (one CloserGroup) → Redis → DB pool → metrics server
// → tracer flush.
func (a *App) Closer(name string, closeFn func(ctx context.Context) error) {
	a.register(func() {
		a.closers = append(a.closers, &closer{name: name, fn: closeFn})
	})
}

// CloserGroup registers ONE teardown step whose members run concurrently. The
// group takes its registration position in the sequential closer order like
// any Closer: the closers registered before it have finished when it starts,
// and the ones registered after it start only when every member has finished
// or the budget is spent.
//
// Each member is reported on its own as "<group>/<member>": its error as
// ErrCloserFailed, its panic as ErrPanic, its overrun as a timeout — and a
// failing member never stops its siblings. Use it for independent closes that
// each block on I/O, the canonical case being Kafka readers and writers: a
// kafka-go consumer-group Reader.Close waits out the in-flight fetch long-poll
// (up to ReaderConfig.MaxWait, default 10 s), so closing N readers one by one
// costs up to N×MaxWait of the budget, concurrently only one MaxWait.
func (a *App) CloserGroup(name string, members ...NamedCloser) {
	a.register(func() {
		a.closers = append(a.closers, &closer{name: name, group: true, members: append([]NamedCloser(nil), members...)})
	})
}

// Stopping returns a channel closed when shutdown begins — before the drain
// delay and before any server stops. Long-lived streams (SSE, WebSocket) that
// a graceful HTTP shutdown would otherwise wait on select on it to end.
func (a *App) Stopping() <-chan struct{} {
	return a.stopping
}

func (a *App) addServer(s *server) {
	s.done = make(chan struct{})
	a.register(func() { a.servers = append(a.servers, s) })
}

// register applies add under the lock, refusing a registration after Run: a
// component added then would never be started or stopped.
func (a *App) register(add func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		panic("lifecycle: component registered after Run")
	}
	add()
}
