package lifecycle

import "errors"

// Sentinels for the failures Run reports. Run's result is an errors.Join of
// wrapped instances, so callers match them with errors.Is. They never cross a
// transport boundary — main maps a non-nil Run result to a non-zero exit code
// — which is why they are plain sentinels and not typed AppErrors.
var (
	// ErrServerExited: a server's start function returned before shutdown
	// began (a bind failure, a crashed listener, or any return at all —
	// http.ErrServerClosed included).
	ErrServerExited = errors.New("lifecycle: server exited before shutdown")

	// ErrWorkerFailed: a worker returned a non-nil error that was not its own
	// context's cancellation.
	ErrWorkerFailed = errors.New("lifecycle: worker failed")

	// ErrPanic: a server, worker, stop function or closer panicked. The panic
	// is recovered, logged with its stack and reported as a failure.
	ErrPanic = errors.New("lifecycle: panic recovered")

	// ErrShutdownTimeout: a component did not finish inside the shutdown
	// budget (a server needed a hard stop, a worker ignored cancellation, a
	// closer overran or was skipped because the budget was already spent).
	ErrShutdownTimeout = errors.New("lifecycle: shutdown budget exceeded")

	// ErrCloserFailed: a closer returned a non-nil error.
	ErrCloserFailed = errors.New("lifecycle: closer failed")
)
