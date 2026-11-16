package lifecycle_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/lifecycle"
)

// SL-05: all phases shared one deadline with no reserve. One handler that never
// returns held the HTTP drain for the whole budget, so the outbox relay's
// in-flight batch, the Dispatcher's in-flight message and every closer (Kafka
// readers, DB pool, tracer flush) were abandoned at process exit.
func TestRun_StuckHandlerDoesNotSpendTheWorkersAndClosersShare(t *testing.T) {
	e := newEcho()
	inHandler := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	e.GET("/stuck", func(c echo.Context) error {
		close(inHandler)
		<-release
		return c.String(http.StatusOK, "late")
	})

	var workerStopped, closerRan atomic.Bool
	logger, logs := observedLogger()
	app := lifecycle.New(logger, lifecycle.WithShutdownBudget(time.Second))
	app.HTTP("http", e, "127.0.0.1:0")
	app.Worker("relay", func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // flushing its in-flight batch
		workerStopped.Store(true)
		return ctx.Err()
	})
	app.Closer("kafka", func(context.Context) error { closerRan.Store(true); return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	base := waitEchoAddr(t, e)
	go func() {
		r, err := http.Get(base + "/stuck") //nolint:gosec // test URL from a local listener
		if err == nil {
			_ = r.Body.Close()
		}
	}()
	<-inHandler
	cancel()

	err := waitResult(t, done, 3*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout, "the stuck handler is still reported")
	assert.True(t, workerStopped.Load(), "the worker must get its share of the budget to finish")
	assert.True(t, closerRan.Load(), "closers must get their share of the budget to run")
	assertShutdownComplete(t, logs, "signal", []string{"server:http"})
}

func TestRun_WorkerReserveOptionCapsServerPhase(t *testing.T) {
	deaf := make(chan struct{})
	defer close(deaf)
	app := lifecycle.New(nil,
		lifecycle.WithShutdownBudget(2*time.Second),
		lifecycle.WithWorkerReserve(1500*time.Millisecond))
	app.Server("deaf", func() error { <-deaf; return nil }, func(context.Context) error { <-deaf; return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()

	_ = waitResult(t, done, 3*time.Second)
	// The deaf server is abandoned when ITS share (budget - reserve = 0.5 s)
	// ends, not at the full budget.
	assert.Less(t, time.Since(start), 1500*time.Millisecond)
}
