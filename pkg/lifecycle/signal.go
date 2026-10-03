package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext returns a context that is cancelled on the first SIGINT or
// SIGTERM. Docker and the kubelet stop a container with SIGTERM, so a process
// that only listens for os.Interrupt is killed by the Go runtime's default
// handler with no drain at all.
//
// Once the first signal has arrived the handler is unregistered, so a second
// signal falls through to the default behaviour and terminates the process
// immediately — the escape hatch for an operator whose shutdown is stuck.
//
// The returned CancelFunc unregisters the handler and cancels the context;
// call it (usually deferred in main) to release the signal registration.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}
