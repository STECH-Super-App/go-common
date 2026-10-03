// Package lifecycle is the fleet's process shutdown orchestrator: one place
// that turns SIGTERM into an ordered, bounded drain instead of every service
// hand-rolling its own main.
//
// It accepts already-built components — an *echo.Echo, a *grpc.Server, worker
// funcs, closers — and builds nothing, reads no configuration and never calls
// os.Exit or a Fatal logger. main keeps ownership of the wiring and of the
// exit code.
//
// # Run contract
//
// Servers and workers start in registration order, each in its own goroutine
// with panic recovery. Shutdown begins on the first of: the Run context ending
// (SIGINT/SIGTERM via SignalContext), any server's start function returning
// (http.ErrServerClosed included — a server must not stop by itself), or a
// worker returning a non-nil error. A worker returning nil early is logged and
// does not trigger shutdown.
//
// Then, inside ONE deadline of WithShutdownBudget (default 25 s) measured from
// the trigger:
//
//  0. Stopping() is closed, then the optional WithDrainDelay elapses.
//  1. Servers stop in parallel, gracefully; a server still busy at the deadline
//     is hard-stopped (Echo Close / gRPC Stop) and reported.
//  2. The workers' context — derived from context.WithoutCancel(ctx), NOT from
//     the signal — is cancelled only now, so Kafka consumers and the outbox
//     relay keep running while in-flight requests drain. Run waits for every
//     worker within the remaining budget.
//  3. Closers run sequentially in REGISTRATION order (not reverse: the order is
//     written down in main, not implied by defers), each with the remaining
//     budget. Recommended: Kafka readers/writers → Redis → DB pool → metrics
//     server → tracer flush.
//
// Run logs "shutdown complete" with the trigger, the duration and the
// components that timed out. It returns nil for a clean signal-triggered
// shutdown and otherwise errors.Join of the triggering failure, every timeout
// (ErrShutdownTimeout), every closer error (ErrCloserFailed) and every worker
// failure seen during shutdown (ErrWorkerFailed). Panics surface as ErrPanic.
//
// # Canonical main
//
//	func main() {
//		ctx, stop := lifecycle.SignalContext(context.Background())
//		defer stop()
//
//		log, err := logger.New(cfg.LogLevel, "example-service")
//		if err != nil {
//			panic(err) // start-up failures before Run may still exit directly
//		}
//		shutdownTracing, err := tracing.Init("example-service")
//		if err != nil {
//			log.Fatal("tracing", zap.Error(err))
//		}
//		pool, err := db.NewPostgres(ctx, cfg.Postgres)
//		if err != nil {
//			log.Fatal("postgres", zap.Error(err))
//		}
//		reader := kafka.NewReader(readerCfg)
//		ob := outbox.New(pool, kafkaWriter, log, outbox.DefaultConfig(), topic)
//		stopMetrics := metrics.StartServer(":9091")
//
//		app := lifecycle.New(log)
//		app.HTTP("http", e, ":8080")
//		app.GRPC("grpc", grpcServer, grpcListener)
//		app.Worker("user-events-consumer", dispatcher.Run)
//		app.Worker("outbox", ob.Run)
//		app.Closer("kafka-reader", func(context.Context) error { return reader.Close() })
//		app.Closer("kafka-writer", func(context.Context) error { return kafkaWriter.Close() })
//		app.Closer("postgres", func(context.Context) error { pool.Close(); return nil })
//		app.Closer("metrics", stopMetrics)
//		app.Closer("tracing", shutdownTracing)
//
//		if err := app.Run(ctx); err != nil {
//			os.Exit(1) // Run has already logged "shutdown complete" with the cause
//		}
//	}
//
// Long-lived streams that a graceful HTTP shutdown would wait on forever (SSE,
// WebSocket) end themselves by selecting on app.Stopping().
package lifecycle
