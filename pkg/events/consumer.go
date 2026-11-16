package events

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/STECH-Super-App/go-common/pkg/config"
	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/kafkawriter"
	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// Reader is the subset of kafka.Reader the dispatcher consumes from.
// Production callers pass a *kafka.Reader directly.
type Reader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Writer is the subset of kafka.Writer used for DLQ and retry destinations.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Dispatcher consumes events, routes them to typed handlers, and manages
// deduplication, in-place retry and the dead-letter tier. Construct with
// NewDispatcher, register handlers with HandleTx or HandlePrepared (or the legacy Handle), then
// call Run(ctx) — or Worker, which maps the outcomes for pkg/lifecycle.
type Dispatcher struct {
	reader Reader
	dlq    Writer
	retry  Writer // legacy retry TIER (WithRetry); nil = retry in place
	dedup  Deduplicator

	maxRetries int // legacy tier budget (WithMaxRetries); ignored without WithRetry

	// In-place retry: a retryable failure is attempted again after a growing,
	// jittered delay until retryWindow has passed since the first failure.
	retryWindow  time.Duration
	retryInitial time.Duration
	retryMax     time.Duration
	// messageTimeout bounds ONE attempt of ONE message in the transaction form:
	// the handler and its transaction.
	messageTimeout time.Duration
	// prepareTimeout and applyTimeout bound the two phases of ONE attempt of a
	// HandlePrepared message (see DefaultPrepareTimeout).
	prepareTimeout time.Duration
	applyTimeout   time.Duration

	log *zap.Logger
	// logProvided records whether WithLogger supplied a real logger. It gates
	// seeding the handler context: a Nop-derived child placed on the context
	// would be FOUND by logger.FromContext and would silently swallow every
	// handler log line, shadowing the process-logger fallback.
	logProvided bool
	group       string
	handlers    map[string]registeredHandler
	// left is set once Run has stopped on a message it did not consume (a
	// handler's ErrLeaveUncommitted, or a failed dead-letter write). From then
	// on Run returns it without fetching: the reader has already moved past that
	// message, so any later fetch-and-commit would consume it.
	left error
}

// GroupUnknown was the group a dispatcher without WithGroup used for its metric
// label AND for its deduplicator claims, so two consumers that both forgot it
// shared claims and silently dropped each other's events. NewDispatcher no longer
// defaults: it takes the group from the reader's GroupID or panics. The constant
// is kept only so code that names it still compiles; nothing applies it.
const GroupUnknown = "unknown"

// Defaults of the in-place retry and the per-message limit. Each is overridable
// by option and, when the option is absent, by the environment variable named
// beside it (read once by NewDispatcher; an unparseable value is logged by
// pkg/config and the default used).
const (
	// DefaultRetryWindow (EVENTS_RETRY_WINDOW) is how long a retryable failure
	// is retried in place, counted from the first failure, before the message is
	// dead-lettered: long enough to
	// ride out a pod restart or a database failover, short enough that a message
	// that can never succeed does not block its partition for long.
	DefaultRetryWindow = 2 * time.Minute
	// DefaultRetryInitialBackoff (EVENTS_RETRY_INITIAL_BACKOFF) is the wait after
	// the first failed attempt; it doubles up to DefaultRetryMaxBackoff
	// (EVENTS_RETRY_MAX_BACKOFF).
	DefaultRetryInitialBackoff = 500 * time.Millisecond
	DefaultRetryMaxBackoff     = 15 * time.Second
	// DefaultMessageTimeout (EVENTS_MESSAGE_TIMEOUT) bounds one attempt: the
	// handler context carries it as a deadline, so a handler stuck on a lock or
	// a dead dependency fails (and is retried) instead of pinning a pooled
	// connection and the partition for ever.
	DefaultMessageTimeout = time.Minute
	// DefaultPrepareTimeout (EVENTS_PREPARE_TIMEOUT) bounds the prepare phase of a
	// HandlePrepared handler: no transaction and no pooled connection is held
	// while it runs, so it may be long — media work such as a video transcode, a
	// rate-limited call to a third party. It also covers the "already processed?"
	// check before it.
	DefaultPrepareTimeout = 10 * time.Minute
	// DefaultApplyTimeout (EVENTS_APPLY_TIMEOUT) bounds the apply phase of a
	// HandlePrepared handler: the claim, apply's writes and the commit, one
	// transaction. It is short on purpose — a hung transaction is cut early —
	// and starts afresh when prepare ends, so a slow prepare never starves it.
	DefaultApplyTimeout = 30 * time.Second
)

// tracerName identifies the instrumentation library in consumer spans.
const tracerName = "github.com/STECH-Super-App/go-common/pkg/events"

// DispatcherOption configures optional dispatcher fields.
type DispatcherOption func(*Dispatcher)

// WithRetry sets a retry TIER writer: the pre-release-2 behaviour, where a
// failed message is forwarded to a retry topic (with an incremented
// x-retry-count) instead of being retried in place.
//
// DEPRECATED (release 2): in-place retry replaces the tier. With WithRetry set
// the dispatcher keeps the old behaviour for retryable failures — forward to the
// tier until WithMaxRetries is spent, then dead-letter — and the dead-letter-
// at-once rules of Classify still apply. A service drops WithRetry (and the
// retry topic, reader and writer) to adopt in-place retry. It is not marked with
// the Go "Deprecated:" convention on purpose: staticcheck would fail every
// service's lint at once, and the fleet moves one service at a time.
func WithRetry(w Writer) DispatcherOption {
	return func(d *Dispatcher) { d.retry = w }
}

// WithMaxRetries overrides the retry budget (default 3) of the legacy retry
// tier. It has no effect without WithRetry.
func WithMaxRetries(n int) DispatcherOption {
	return func(d *Dispatcher) { d.maxRetries = n }
}

// WithRetryWindow sets how long a retryable failure is retried in place before
// the message is dead-lettered (default DefaultRetryWindow). Non-positive keeps
// the default.
func WithRetryWindow(window time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if window > 0 {
			d.retryWindow = window
		}
	}
}

// WithRetryBackoff sets the first and the largest wait between in-place
// attempts (defaults DefaultRetryInitialBackoff, DefaultRetryMaxBackoff). A
// non-positive value keeps its default.
func WithRetryBackoff(initial, maxWait time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if initial > 0 {
			d.retryInitial = initial
		}
		if maxWait > 0 {
			d.retryMax = maxWait
		}
	}
}

// WithMessageTimeout sets the time limit of one attempt at one message
// (default DefaultMessageTimeout). Non-positive keeps the default.
func WithMessageTimeout(limit time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if limit > 0 {
			d.messageTimeout = limit
		}
	}
}

// WithPrepareTimeout sets the time limit of the prepare phase of a
// HandlePrepared handler (default DefaultPrepareTimeout). Non-positive keeps the
// default. It does not apply to HandleTx handlers (WithMessageTimeout does).
func WithPrepareTimeout(limit time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if limit > 0 {
			d.prepareTimeout = limit
		}
	}
}

// WithApplyTimeout sets the time limit of the apply phase of a HandlePrepared
// handler — its transaction (default DefaultApplyTimeout). Non-positive keeps the
// default. It does not apply to HandleTx handlers.
func WithApplyTimeout(limit time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if limit > 0 {
			d.applyTimeout = limit
		}
	}
}

// WithLogger supplies a zap logger. Default: zap.NewNop().
//
// Supplying one also enables the request-scoped logger on the handler context
// (see handleOne): without it, the dispatcher has no base logger to derive a
// child from, and handlers fall back to the process logger instead.
func WithLogger(l *zap.Logger) DispatcherOption {
	return func(d *Dispatcher) {
		if l == nil {
			return
		}
		d.log = l
		d.logProvided = true
	}
}

// WithGroup supplies the consumer group id used as the "group" label on every
// §3.4 consumer metric AND as the consumer name of the deduplicator's claims.
// Optional only when the reader exposes its GroupID (a *kafka.Reader built with
// one: the dispatcher reads it); a reader that does not and no WithGroup is a
// panic in NewDispatcher — a shared default would make two dispatchers share
// claims.
//
// Pass the Kafka GroupID VERBATIM — the exact string the kafka.ReaderConfig
// got, whether it came from env or a literal. It is a contract, not a naming
// choice: only the Kafka GroupID matches what kafka-exporter reports in its
// consumergroup label, which is what lets a lag panel and a dead-letter
// counter sit on the same dashboard row. Do not pass the DLQ short name (a
// repo can have both, and they differ: "order-review-events-consumer" vs
// "order").
func WithGroup(id string) DispatcherOption {
	return func(d *Dispatcher) {
		if id != "" {
			d.group = id
		}
	}
}

// NewDispatcher constructs a Dispatcher. reader, dlq and dedup are required —
// no code path drops a failed message on the floor, and no consumer runs
// without deduplication:
//
//   - dedup is a Deduplicator, whose only values come from this package
//     (NewPostgresDeduplicator in production, NewMemoryDeduplicator in tests).
//     A nil one — including a typed nil such as (*PostgresDeduplicator)(nil) —
//     panics.
//   - the consumer group is the reader's GroupID (a *kafka.Reader exposes it), or
//     WithGroup's. With neither NewDispatcher panics: the group names the
//     deduplicator's claims, and a shared default would let two dispatchers
//     swallow each other's events.
//   - A failed message is retried in place (see Classify) and then written to
//     dlq; a handler panic is not retried at all (ClassPanic: written to dlq at
//     once, reason handler_panic, stack in the log); if THAT write fails the dispatcher stops (Run returns an error
//     wrapping ErrDeadLetterWriteFailed) without committing — it never moves
//     on and loses the message.
//   - A handler's ErrLeaveUncommitted also stops Run, without any write.
func NewDispatcher(reader Reader, dlq Writer, dedup Deduplicator, opts ...DispatcherOption) *Dispatcher {
	if isNilDeduplicator(dedup) {
		panic(internalError(ReasonDedupFailed, "events.NewDispatcher: dedup is nil: every consumer needs a Deduplicator (events.NewPostgresDeduplicator)", nil))
	}
	d := &Dispatcher{
		reader:         reader,
		dlq:            dlq,
		dedup:          dedup,
		maxRetries:     3,
		retryWindow:    config.GetEnvDuration("EVENTS_RETRY_WINDOW", DefaultRetryWindow),
		retryInitial:   config.GetEnvDuration("EVENTS_RETRY_INITIAL_BACKOFF", DefaultRetryInitialBackoff),
		retryMax:       config.GetEnvDuration("EVENTS_RETRY_MAX_BACKOFF", DefaultRetryMaxBackoff),
		messageTimeout: config.GetEnvDuration("EVENTS_MESSAGE_TIMEOUT", DefaultMessageTimeout),
		prepareTimeout: config.GetEnvDuration("EVENTS_PREPARE_TIMEOUT", DefaultPrepareTimeout),
		applyTimeout:   config.GetEnvDuration("EVENTS_APPLY_TIMEOUT", DefaultApplyTimeout),
		log:            zap.NewNop(),
		handlers:       make(map[string]registeredHandler),
	}
	for _, o := range opts {
		o(d)
	}
	if d.group == "" {
		d.group = readerGroupID(reader)
	}
	if d.group == "" {
		panic(internalError(ReasonDedupFailed, "events.NewDispatcher: no consumer group: pass events.WithGroup(<the reader's GroupID>) — "+
			"it names the deduplicator's claims, and two dispatchers without one would share them and drop each other's events", nil))
	}
	checkTierWriter("DLQ", d.dlq)
	checkTierWriter("retry", d.retry)
	return d
}

// isNilDeduplicator reports a nil Deduplicator, typed nil included: a
// (*PostgresDeduplicator)(nil) is a non-nil interface that fails on first use.
func isNilDeduplicator(d Deduplicator) bool {
	if d == nil {
		return true
	}
	v := reflect.ValueOf(d)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// readerGroupID is the GroupID of a reader that exposes its configuration
// (*kafka.Reader), or "".
func readerGroupID(r Reader) string {
	if r == nil {
		return ""
	}
	if v := reflect.ValueOf(r); v.Kind() == reflect.Pointer && v.IsNil() {
		return ""
	}
	if c, ok := r.(interface{ Config() kafka.ReaderConfig }); ok {
		return c.Config().GroupID
	}
	return ""
}

// checkTierWriter refuses an unsafe *kafka.Writer used as a failure tier.
func checkTierWriter(tier string, w Writer) {
	if kw, ok := w.(*kafka.Writer); ok {
		kafkawriter.MustCheck("events.NewDispatcher "+tier+" writer", kw)
	}
}

// handlerFn is the registered form of a transaction-form handler: it decodes the
// payload and calls the typed function. tx is the deduplicator's transaction
// (nil under MemoryDeduplicator); a legacy handler ignores it.
type handlerFn func(ctx context.Context, tx Tx, rawPayload []byte, headers envelope.Headers) error

// prepareFn is the registered first phase of a HandlePrepared handler: it
// decodes the payload, runs the typed prepare and returns the second phase as a
// closure over the decoded message and prepare's result.
type prepareFn func(ctx context.Context, rawPayload []byte) (apply func(ctx context.Context, tx Tx) error, err error)

// registeredHandler is what the dispatcher routes to: exactly one field is set.
type registeredHandler struct {
	tx      handlerFn
	prepare prepareFn
}

// HandleTx registers a typed handler in the TRANSACTION form: the dispatcher
// opens a transaction, records the event id in it, calls fn with that
// transaction and commits once when fn returns nil. The event's effect and its
// "processed" mark are one atomic unit: a failed commit or a crash leaves
// neither, and the retry runs fn again on a clean slate (SL-04).
//
// fn must do every database write — and publish every outbox event
// (outbox.Publisher.PublishProto takes the same tx) — through tx, and pass tx
// down to the methods it calls; a write through the pool is a second
// transaction and is not covered. A write to anything that is not Postgres (an
// HTTP call, a push) is not either, and stays at-least-once: make it idempotent.
//
// The message is routed when its event_type header equals
// proto.MessageName(*new(T)).
func HandleTx[T proto.Message](d *Dispatcher, fn func(ctx context.Context, tx Tx, msg T) error) {
	var zero T
	fqn := string(proto.MessageName(zero))
	if fqn == "" {
		panic("events.HandleTx: proto.MessageName returned empty (nil T?)")
	}
	d.handlers[fqn] = registeredHandler{tx: func(ctx context.Context, tx Tx, raw []byte, _ envelope.Headers) error {
		msg, err := decodeProto[T](zero, raw)
		if err != nil {
			return err
		}
		return fn(ctx, tx, msg)
	}}
}

// decodeProto unmarshals a payload into a new T; a failure is the decode class.
func decodeProto[T proto.Message](zero T, raw []byte) (T, error) {
	msg := zero.ProtoReflect().New().Interface().(T)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, msg); err != nil {
		return msg, fmt.Errorf("%w: %v", errUnmarshal, err)
	}
	return msg, nil
}

// HandlePrepared registers a typed handler in the TWO-PHASE form, for a handler
// whose slow work is not a database write — an ffmpeg run, a call to a third
// party — and which writes only afterwards. It keeps every guarantee of HandleTx
// (the effect and the "processed" mark are one atomic unit, per-message retry,
// dead letters, panic recovery) without holding a transaction, or the pooled
// connection under it, through the slow part:
//
//  1. a read-only check: an event that is already processed skips prepare
//     entirely (no transaction is held after it);
//  2. prepare(ctx, msg) runs with NO transaction and NO connection held by this
//     library. Its error is classified exactly like a handler's (Classify:
//     retried in place, poison pill, validation, ErrLeaveUncommitted). It is
//     bounded by WithPrepareTimeout (default DefaultPrepareTimeout);
//  3. the dispatcher opens a transaction, claims the event id (the same
//     processed_events insert as HandleTx; if a concurrent delivery got there
//     first, apply is skipped and p discarded), calls apply(ctx, tx, msg, p) and
//     commits once. apply does every database write and outbox publish through
//     tx, like a HandleTx handler; it is bounded by WithApplyTimeout (default
//     DefaultApplyTimeout), counted from the end of prepare.
//
// P is whatever prepare hands to apply (a path, a computed result); use
// struct{} when the slow phase returns nothing. Neither phase may keep p or tx
// after it returns.
//
// ONE NEW OBLIGATION: prepare must be idempotent, and safe to run more than
// once, even at the same time. It runs again after an apply that failed or timed
// out, after a commit that failed, after a crash between the phases, and in two
// pods when a duplicate is delivered at once (only one of them applies). The
// library caches p across NO attempt: every retry runs prepare afresh. Side
// effects of prepare that are not repeatable belong in apply — or must be keyed
// so that repeating them is harmless.
//
// Use HandleTx when the whole handler is database work; use HandlePrepared when
// slow external work comes first; never do slow I/O inside HandleTx, where it
// holds an open, idle transaction (a pinned connection, held-back vacuum).
//
// The message is routed when its event_type header equals
// proto.MessageName(*new(T)). A nil prepare or apply panics.
func HandlePrepared[T proto.Message, P any](
	d *Dispatcher,
	prepare func(ctx context.Context, msg T) (P, error),
	apply func(ctx context.Context, tx Tx, msg T, p P) error,
) {
	if prepare == nil || apply == nil {
		panic("events.HandlePrepared: prepare and apply are both required")
	}
	var zero T
	fqn := string(proto.MessageName(zero))
	if fqn == "" {
		panic("events.HandlePrepared: proto.MessageName returned empty (nil T?)")
	}
	d.handlers[fqn] = registeredHandler{prepare: func(ctx context.Context, raw []byte) (func(context.Context, Tx) error, error) {
		msg, err := decodeProto[T](zero, raw)
		if err != nil {
			return nil, err
		}
		p, err := prepare(ctx, msg)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, tx Tx) error { return apply(ctx, tx, msg, p) }, nil
	}}
}

// Handle registers a typed handler in the LEGACY form. The event id is still
// deduplicated, but the handler gets no transaction, so its writes commit
// separately from the "processed" mark: a failed commit of the mark runs the
// handler a second time (SL-04). It exists so a service can adopt release 2
// without converting every handler at once; convert each to HandleTx, then ban
// the old form with lintguards.LegacyEventHandlers.
//
// DEPRECATED (release 2) — see WithRetry for why this is not the Go
// "Deprecated:" marker.
//
// The message is routed when its event_type header equals
// proto.MessageName(*new(T)).
func Handle[T proto.Message](d *Dispatcher, fn func(context.Context, T) error) {
	HandleTx(d, func(ctx context.Context, _ Tx, msg T) error { return fn(ctx, msg) })
}

// Internal sentinels for failure classification.
var (
	errUnmarshal = errors.New("unmarshal")
	errPanic     = errors.New("panic")
)

// panicError is what a recovered handler panic becomes: an error of class
// ClassPanic (it wraps errPanic) that keeps the stack of the panic, which the
// dispatcher logs when it dead-letters the message. The panic VALUE is only
// formatted into the message, never wrapped: a handler that panics with an
// error wrapping ErrPoisonPill or ErrLeaveUncommitted is still a panic.
type panicError struct {
	value any
	stack []byte
}

func newPanicError(value any) *panicError {
	return &panicError{value: value, stack: debug.Stack()}
}

func (p *panicError) Error() string { return fmt.Sprintf("%v: %v", errPanic, p.value) }
func (p *panicError) Unwrap() error { return errPanic }

// dlqReason categorizes a dead letter for the x-dlq-reason header and the
// events_consumer_dead_lettered_total{reason} label.
type dlqReason string

const (
	reasonMaxRetries   dlqReason = "max_retries"
	reasonPoisonPill   dlqReason = "poison_pill"
	reasonUnmarshal    dlqReason = "unmarshal_error"
	reasonValidation   dlqReason = "validation"
	reasonHandlerPanic dlqReason = "handler_panic"
)

// Fetch-error backoff bounds: the first retry waits fetchBackoffInitial, each
// consecutive failure doubles it up to fetchBackoffCap, and a successful fetch
// resets it.
const (
	fetchBackoffInitial = 100 * time.Millisecond
	fetchBackoffCap     = 5 * time.Second
)

// Run polls the reader until ctx is cancelled, the reader is closed, or the
// dispatcher must stop on a message it did not consume. Each message is routed
// to its registered handler; on error, the retry and dead-letter rules apply.
// Messages are handled one at a time, which the commit guarantees below rely
// on, so Run is not for concurrent use on one Dispatcher.
//
// Shutdown finishes the message in flight. ctx governs only the FETCH of the
// next message and the WAIT between in-place retries: it is checked before every
// fetch and passed to FetchMessage, so a cancel interrupts an idle, blocking
// fetch at once. A message that has already been fetched is handled to
// completion on context.WithoutCancel(ctx) — the handler, the dedup transaction,
// the dead-letter write and the offset commit all run on a context that keeps
// ctx's values (trace, logger) but not its cancellation. Cancelling them
// mid-message is what produces duplicate side effects: the handler's SMS has
// left the provider, then the dead ctx rolls back the dedup claim and fails the
// offset commit, and Kafka redelivers it. That work is bounded by
// WithMessageTimeout per attempt and, above it, by pkg/lifecycle's shutdown
// budget. A shutdown that arrives while the dispatcher waits to retry a failed
// message stops it there, leaving the message uncommitted for redelivery. Run
// returns as soon as the message is done and never starts another once ctx has
// ended.
//
// The ctx check before each fetch is load-bearing, not redundant with passing
// ctx to FetchMessage: kafka-go picks at random between a buffered message and
// ctx.Done when both are ready, so a cancelled fetch can still hand out — and
// Run would then fully process — one more message per loop.
//
// Run stops, permanently, without committing and without fetching again, when
//
//   - a handler returns an error wrapping ErrLeaveUncommitted (or a shutdown
//     interrupts an in-place retry): Run returns an error wrapping
//     ErrLeaveUncommitted; or
//   - the dead-letter write of a failed message fails (SL-00): Run returns an
//     error wrapping ErrDeadLetterWriteFailed. The process must exit non-zero
//     so the restart redelivers the message — Worker does that wiring.
//
// The stop is mandatory, not a policy: kafka-go commits a partition by position
// (offset+1), so committing any later message of that partition would mark the
// one left behind as consumed. For the same reason it is permanent — the reader
// has already moved past that message, so a later Run on this Dispatcher returns
// the same error without fetching. Kafka redelivers the message once this reader
// is closed and its partition moves to another consumer, or to this service
// after its restart.
//
// Returns ctx.Err() on cancel; nil when the reader has been closed (kafka-go
// answers every FetchMessage on a closed reader with io.EOF — a closed reader
// is a shutdown, not a fault, and retrying it would spin forever); and the
// stop errors above. Any other fetch error is logged and retried with a capped
// exponential backoff, so a broker outage is not a hot loop.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d.left != nil {
		return d.left
	}
	backoff := fetchBackoffInitial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := d.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, io.EOF) {
				d.log.Info("reader closed; dispatcher stopping", zap.String("group", d.group))
				return nil
			}
			d.log.Warn("fetch error", zap.Error(err), zap.Duration("retry_in", backoff))
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = nextFetchBackoff(backoff)
			continue
		}
		backoff = fetchBackoffInitial
		if err := d.handleOne(context.WithoutCancel(ctx), ctx.Done(), msg); err != nil {
			d.left = err
			return err
		}
	}
}

// Worker adapts Run to a pkg/lifecycle worker:
//
//	app.Worker("user-events", disp.Worker(app.Stopping()))
//
// It returns context.Canceled-compatible nil/ctx errors on an orderly shutdown
// and an error for everything lifecycle must treat as a failed worker, which
// makes the process exit non-zero so Kubernetes restarts it LOUDLY:
//
//   - the dead-letter write failed (ErrDeadLetterWriteFailed): always an error;
//   - the reader was closed (EOF) while the process is not stopping
//     (ErrReaderClosed): an error — a dead consumer must restart the pod;
//   - the handler left a message uncommitted (ErrLeaveUncommitted): an error
//     while the process is running — lifecycle takes a worker's nil before
//     shutdown as "returned early; continuing", which would leave the pod alive
//     with a dispatcher that is stopped for good but whose reader still holds
//     its partitions — and nil once stopping is closed, because then it is part
//     of the shutdown, not a fault.
//
// stopping is lifecycle.App.Stopping().
func (d *Dispatcher) Worker(stopping <-chan struct{}) func(context.Context) error {
	return func(ctx context.Context) error {
		err := d.Run(ctx)
		if err == nil {
			// Run returns nil for a closed reader. That is a shutdown only when
			// the process is shutting down; otherwise the consumer is dead and the
			// pod must restart (see ErrReaderClosed).
			select {
			case <-stopping:
				return nil
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return internalError(ReasonReaderClosed,
				"events: the Kafka reader was closed while the process is running; the consumer is dead",
				fmt.Errorf("%w (group %s)", ErrReaderClosed, d.group))
		}
		if errors.Is(err, ErrDeadLetterWriteFailed) {
			return err
		}
		if ctx.Err() != nil && !errors.Is(err, ErrLeaveUncommitted) {
			return ctx.Err()
		}
		if errors.Is(err, ErrLeaveUncommitted) {
			select {
			case <-stopping:
				return nil
			default:
			}
		}
		return err
	}
}

// nextFetchBackoff doubles the wait, capped at fetchBackoffCap.
func nextFetchBackoff(current time.Duration) time.Duration {
	return min(current*2, fetchBackoffCap)
}

// sleepCtx waits for d or until ctx ends; it reports whether the full wait
// elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// handleOne takes one fetched message to its end: commit it, dead-letter it, or
// — when it cannot be finished — stop without consuming it. stop is closed when
// the process is shutting down (ctx.Done of Run). It returns a non-nil error
// only in the stop cases, and Run then stops.
func (d *Dispatcher) handleOne(ctx context.Context, stop <-chan struct{}, msg kafka.Message) error {
	h := envelope.FromKafka(msg.Headers)
	fqn := h.EventType()

	// event_id and topic are the two fields that make a consumer log line
	// searchable — the consumer-path equivalent of the request_id an HTTP line
	// carries.
	msgLog := d.log.With(
		zap.String("event_id", h.EventID()),
		zap.String("topic", msg.Topic),
	)

	handler, ok := d.handlers[fqn]
	if !ok {
		// Skipping is legitimate for a consumer that handles only some of a
		// topic's event types, so it is Debug — but it is counted, because a
		// renamed proto message (the routing key is its fully-qualified name)
		// makes every event skip here with lag 0 and nothing else moving
		// (SL-08). A MISSING header is never legitimate: a producer forgot it.
		unhandledTotal.WithLabelValues(msg.Topic, d.group).Inc()
		if fqn == "" {
			msgLog.Warn("message has no event_type header; skipping",
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset))
		} else {
			msgLog.Debug("no handler for event type; skipping",
				zap.String("event_type", fqn))
		}
		d.commit(ctx, msg, msgLog)
		return nil
	}

	// Attach envelope headers to the context so handlers can read fields that
	// don't appear in the proto payload (occurred_at, retry-count, ...). The
	// typed handler signature carries only the message — handlers fish the
	// headers out via envelope.HeadersFromContext when they need them.
	ctx = envelope.WithHeaders(ctx, h)

	ctx, span := d.startConsumerSpan(ctx, msg, h, fqn)
	defer span.End()

	if sc := span.SpanContext(); sc.IsValid() {
		msgLog = msgLog.With(
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}
	// Only seed the context logger when there is a real base logger to derive
	// from. With no WithLogger, storing the Nop-derived child would make
	// logger.FromContext return it — swallowing handler logs — instead of
	// resolving to the process logger built by logger.New.
	if d.logProvided {
		ctx = logger.IntoContext(ctx, msgLog)
	}

	// The retry window runs from the FIRST FAILURE, not from the first attempt: a
	// handler whose attempt takes minutes (a HandlePrepared prepare phase) must
	// still get its retries when that attempt fails.
	var firstFailure time.Time
	for attempt := 1; ; attempt++ {
		ran, err := d.attempt(ctx, msg, h, handler)
		if err == nil {
			if ran {
				processedTotal.WithLabelValues(msg.Topic, d.group).Inc()
			} else {
				dedupHitsTotal.WithLabelValues(msg.Topic, d.group).Inc()
			}
			d.commit(ctx, msg, msgLog)
			return nil
		}

		// The handler asked for this message not to be consumed: no commit, no
		// retry or DLQ copy, no failure counted. Checked before Classify, so it
		// outranks ErrPoisonPill. The deduplicator has already rolled its claim
		// back, having seen this same error.
		if errors.Is(err, ErrLeaveUncommitted) {
			span.AddEvent("message left uncommitted")
			msgLog.Warn("handler left the message uncommitted; dispatcher stopping",
				zap.String("group", d.group),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err))
			return d.leftUncommitted(msg, err)
		}

		span.RecordError(err)
		handlerFailuresTotal.WithLabelValues(msg.Topic, d.group).Inc()
		if firstFailure.IsZero() {
			firstFailure = time.Now()
		}

		class := Classify(err)
		if class == ClassPanic {
			logPanic(msgLog, msg, err)
		}
		if class.DeadLetterNow() {
			return d.deadLetter(ctx, msg, msgLog, err, reasonOf(class))
		}

		// Retryable. The legacy tier, when configured, takes the message off
		// the partition at once.
		if d.retry != nil {
			return d.retryTier(ctx, msg, h, msgLog, err)
		}

		delay := d.retryDelay(attempt)
		if time.Since(firstFailure)+delay > d.retryWindow {
			msgLog.Error("retries exhausted; dead-lettering the message",
				zap.Int("attempts", attempt),
				zap.Duration("window", d.retryWindow),
				zap.String("group", d.group),
				zap.Error(err))
			return d.deadLetter(ctx, msg, msgLog, err, reasonOf(class))
		}
		inPlaceRetriesTotal.WithLabelValues(msg.Topic, d.group).Inc()
		msgLog.Warn("handler failed; retrying in place",
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", delay),
			zap.Error(err))
		if !waitOrStop(delay, stop) {
			// The process is shutting down. The message is not lost: nothing is
			// committed, and Kafka redelivers it after the restart.
			span.AddEvent("shutdown during in-place retry")
			msgLog.Warn("shutdown during in-place retry; leaving the message uncommitted",
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset))
			return d.leftUncommitted(msg, errors.New("shutdown during in-place retry"))
		}
	}
}

// logPanic logs a recovered handler panic with the stack captured where it
// happened: the dead letter carries only the message of the panic, so the log is
// the one place the stack is kept.
func logPanic(msgLog *zap.Logger, msg kafka.Message, err error) {
	fields := []zap.Field{
		zap.Int("partition", msg.Partition),
		zap.Int64("offset", msg.Offset),
		zap.Error(err),
	}
	var pe *panicError
	if errors.As(err, &pe) {
		fields = append(fields, zap.ByteString("stack", pe.stack))
	}
	msgLog.Error("handler panicked; dead-lettering the message without a retry", fields...)
}

// attempt runs the handler once — inside the deduplicator's unit and under the
// per-message time limit for a transaction-form handler, in two phases for a
// two-phase one. ran is false when the event had already been processed.
func (d *Dispatcher) attempt(ctx context.Context, msg kafka.Message, h envelope.Headers, handler registeredHandler) (ran bool, err error) {
	if handler.prepare != nil {
		return d.attemptPrepared(ctx, msg, h, handler.prepare)
	}
	ctx, cancel := context.WithTimeout(ctx, d.messageTimeout)
	defer cancel()

	ran, err = d.dedup.process(ctx, d.group, h.EventID(), func(ctx context.Context, tx Tx) error {
		return d.safelyInvoke(ctx, handler.tx, tx, msg.Value, h)
	})
	return ran, timedOut(ctx, err, "handler", "message", d.messageTimeout)
}

// attemptPrepared is one attempt of a HandlePrepared handler (see there). The
// read-only check and prepare run under prepareTimeout and hold no connection;
// the transaction exists only inside the second, short, apply-timeout context.
func (d *Dispatcher) attemptPrepared(ctx context.Context, msg kafka.Message, h envelope.Headers, prepare prepareFn) (ran bool, err error) {
	eventID := h.EventID()

	prepCtx, cancelPrep := context.WithTimeout(ctx, d.prepareTimeout)
	defer cancelPrep()
	// An event without an id cannot be deduplicated: process it (see the
	// deduplicator), without the early exit.
	if eventID != "" {
		done, err := d.dedup.processed(prepCtx, d.group, eventID)
		if err != nil {
			return false, timedOut(prepCtx, err, "prepare", "prepare", d.prepareTimeout)
		}
		if done {
			return false, nil
		}
	}
	apply, err := d.safelyPrepare(prepCtx, prepare, msg.Value)
	if err != nil {
		return true, timedOut(prepCtx, err, "prepare", "prepare", d.prepareTimeout)
	}
	cancelPrep() // the slow phase is over; nothing may keep running on its deadline

	applyCtx, cancelApply := context.WithTimeout(ctx, d.applyTimeout)
	defer cancelApply()
	ran, err = d.dedup.process(applyCtx, d.group, eventID, func(ctx context.Context, tx Tx) error {
		return safelyApply(ctx, apply, tx)
	})
	return ran, timedOut(applyCtx, err, "apply", "apply", d.applyTimeout)
}

// timedOut says that a per-message limit was hit, keeping the original error in
// the chain; an error that is already dead-letter-at-once keeps its class.
func timedOut(ctx context.Context, err error, phase, name string, limit time.Duration) error {
	if err != nil && ctx.Err() == context.DeadlineExceeded && !Classify(err).DeadLetterNow() {
		return fmt.Errorf("%w: %w", internalError(ReasonMessageTimeout,
			fmt.Sprintf("events: %s exceeded the %s %s limit", phase, limit, name), nil), err)
	}
	return err
}

// reasonOf maps a classified failure to its x-dlq-reason.
func reasonOf(class Class) dlqReason {
	switch class {
	case ClassDecode:
		return reasonUnmarshal
	case ClassPoisonPill:
		return reasonPoisonPill
	case ClassValidation:
		return reasonValidation
	case ClassPanic:
		return reasonHandlerPanic
	default:
		return reasonMaxRetries
	}
}

// retryDelay is the wait after the attempt-th failed attempt: the initial
// backoff doubled per attempt, capped, with +-20% jitter so consumers that
// failed together do not retry together.
func (d *Dispatcher) retryDelay(attempt int) time.Duration {
	delay := d.retryInitial
	for i := 1; i < attempt && delay < d.retryMax; i++ {
		delay *= 2
	}
	delay = min(delay, d.retryMax)
	jitter := 0.8 + 0.4*rand.Float64() //nolint:gosec // jitter, not security
	return time.Duration(float64(delay) * jitter)
}

// waitOrStop waits for delay; it reports false when stop closed first.
func waitOrStop(delay time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}

// leftUncommitted builds the error Run stops with when a message is left for
// redelivery.
func (d *Dispatcher) leftUncommitted(msg kafka.Message, cause error) error {
	return fmt.Errorf("events: dispatcher stopped; %s partition %d offset %d left uncommitted: %w: %w",
		msg.Topic, msg.Partition, msg.Offset, ErrLeaveUncommitted, cause)
}

// deadLetter writes the message to the DLQ and commits it. When the write
// fails it STOPS: nothing is committed, nothing more is fetched, and Run returns
// an error wrapping ErrDeadLetterWriteFailed (SL-00). Moving on would let the
// next successful commit on the partition commit PAST this message, which
// would be lost with lag 0 and no alert.
func (d *Dispatcher) deadLetter(ctx context.Context, msg kafka.Message, msgLog *zap.Logger, cause error, reason dlqReason) error {
	if werr := d.writeDLQ(ctx, msg, cause, reason); werr != nil {
		return d.writeFailed(msg, msgLog, "dead-letter", reason, werr)
	}
	d.commit(ctx, msg, msgLog)
	return nil
}

// retryTier is the legacy WithRetry path for a retryable failure: forward to the
// retry tier while the budget lasts, else dead-letter.
func (d *Dispatcher) retryTier(ctx context.Context, msg kafka.Message, h envelope.Headers, msgLog *zap.Logger, cause error) error {
	retryCount := h.RetryCount()
	if retryCount >= d.maxRetries {
		return d.deadLetter(ctx, msg, msgLog, cause, reasonOf(ClassRetryable))
	}
	next := kafkaCloneWithHeader(msg, envelope.HeaderRetryCount, fmt.Sprintf("%d", retryCount+1))
	if werr := d.retry.WriteMessages(ctx, next); werr != nil {
		return d.writeFailed(msg, msgLog, "retry", reasonMaxRetries, werr)
	}
	retriedTotal.WithLabelValues(msg.Topic, d.group).Inc()
	d.commit(ctx, msg, msgLog)
	return nil
}

// writeFailed is the stop path of a failed dead-letter (or retry-tier) write.
func (d *Dispatcher) writeFailed(msg kafka.Message, msgLog *zap.Logger, tier string, reason dlqReason, werr error) error {
	failurePathErrorsTotal.WithLabelValues(msg.Topic, d.group).Inc()
	msgLog.Error(tier+" write failed; stopping the consumer without committing so the message is redelivered after the restart",
		zap.String("dlq_reason", string(reason)),
		zap.String("group", d.group),
		zap.Int("partition", msg.Partition),
		zap.Int64("offset", msg.Offset),
		zap.Error(werr))
	return internalError(ReasonDeadLetterWriteFailed,
		fmt.Sprintf("events: %s write failed for %s partition %d offset %d; consumer stopped, message left uncommitted",
			tier, msg.Topic, msg.Partition, msg.Offset),
		fmt.Errorf("%w: %w", ErrDeadLetterWriteFailed, werr))
}

// commit commits msg's offset. A failure is logged and counted, not returned:
// the handler already ran (or the failure tier already holds the message), and
// Kafka redelivers an uncommitted offset to the dedup claim, which skips it.
// What it must not be is silent (SL-08).
func (d *Dispatcher) commit(ctx context.Context, msg kafka.Message, msgLog *zap.Logger) {
	if err := d.reader.CommitMessages(ctx, msg); err != nil {
		commitErrorsTotal.WithLabelValues(msg.Topic, d.group).Inc()
		msgLog.Error("offset commit failed",
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Error(err))
	}
}

// startConsumerSpan continues the producing request's trace across the Kafka
// hop: the traceparent header was injected at PublishProto time from the
// context that caused the event, so the handler span becomes a child of that
// request rather than a disconnected root.
//
// The span name is the topic, never the event id — span names obey the same
// cardinality law as metric labels (trap §9.18).
func (d *Dispatcher) startConsumerSpan(
	ctx context.Context,
	msg kafka.Message,
	h envelope.Headers,
	eventType string,
) (context.Context, trace.Span) {
	// Trace context only, whatever the process propagator is: a baggage header
	// on a message is not ours to honour (SL-24).
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier(h))

	return otel.Tracer(tracerName).Start(ctx,
		"consume "+msg.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", msg.Topic),
			attribute.String("messaging.consumer.group.name", d.group),
			attribute.String("event_type", eventType),
			attribute.String("event_id", h.EventID()),
		),
	)
}

// safelyPrepare runs a handler's first phase, turning a panic into an errPanic
// error like safelyInvoke does.
func (d *Dispatcher) safelyPrepare(ctx context.Context, prepare prepareFn, raw []byte) (apply func(context.Context, Tx) error, err error) {
	defer func() {
		if r := recover(); r != nil {
			apply, err = nil, newPanicError(r)
		}
	}()
	return prepare(ctx, raw)
}

// safelyApply runs a handler's second phase, turning a panic into an errPanic
// error; the deduplicator's transaction then rolls back with it.
func safelyApply(ctx context.Context, apply func(context.Context, Tx) error, tx Tx) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = newPanicError(r)
		}
	}()
	return apply(ctx, tx)
}

func (d *Dispatcher) safelyInvoke(ctx context.Context, h handlerFn, tx Tx, raw []byte, hdr envelope.Headers) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = newPanicError(r)
		}
	}()
	return h(ctx, tx, raw, hdr)
}

// writeDLQ publishes the failed message to the DLQ topic with diagnostic
// headers and returns the write error, if any. A non-nil return makes
// deadLetter stop the dispatcher (the source offset stays uncommitted).
func (d *Dispatcher) writeDLQ(ctx context.Context, src kafka.Message, err error, reason dlqReason) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	headers := make([]kafka.Header, 0, len(src.Headers)+5)
	headers = append(headers, src.Headers...)

	setOrReplace := func(k, v string) {
		for i, h := range headers {
			if h.Key == k {
				headers[i].Value = []byte(v)
				return
			}
		}
		headers = append(headers, kafka.Header{Key: k, Value: []byte(v)})
	}

	setOrReplace("x-dlq-reason", string(reason))
	setOrReplace("x-dlq-error", truncate(err.Error(), 1024))
	setOrReplace("x-dlq-last-seen-at", now)

	firstSeen := ""
	for _, h := range src.Headers {
		if h.Key == "x-dlq-first-seen-at" {
			firstSeen = string(h.Value)
		}
	}
	if firstSeen == "" {
		firstSeen = now
	}
	setOrReplace("x-dlq-first-seen-at", firstSeen)

	dlqMsg := kafka.Message{
		Key:     src.Key,
		Value:   src.Value,
		Headers: headers,
	}
	if werr := d.dlq.WriteMessages(ctx, dlqMsg); werr != nil {
		d.log.Error("dlq write failed",
			zap.String("event_id", string(findHeader(src.Headers, envelope.HeaderEventID))),
			zap.String("topic", src.Topic),
			zap.Error(werr))
		return werr
	}
	// The topic label is the SOURCE topic the message failed on, which is what
	// pairs this counter with the consumer-lag panel for the same topic.
	deadLetteredTotal.WithLabelValues(src.Topic, d.group, string(reason)).Inc()
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func kafkaCloneWithHeader(src kafka.Message, key, val string) kafka.Message {
	headers := make([]kafka.Header, 0, len(src.Headers)+1)
	replaced := false
	for _, h := range src.Headers {
		if h.Key == key {
			headers = append(headers, kafka.Header{Key: key, Value: []byte(val)})
			replaced = true
			continue
		}
		headers = append(headers, h)
	}
	if !replaced {
		headers = append(headers, kafka.Header{Key: key, Value: []byte(val)})
	}
	return kafka.Message{Key: src.Key, Value: src.Value, Headers: headers}
}

func findHeader(headers []kafka.Header, key string) []byte {
	for _, h := range headers {
		if h.Key == key {
			return h.Value
		}
	}
	return nil
}
