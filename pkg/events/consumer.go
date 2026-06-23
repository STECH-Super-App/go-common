package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
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

// Deduplicator is the subset of outbox.Deduplicator the dispatcher uses.
// Kept as an interface here to avoid a circular import between events and outbox.
//
// Process must run fn inside the same atomic claim-and-record unit so that
// concurrent deliveries of the same eventID across consumer pods see
// exactly-once semantics: one pod's fn runs and commits, the others see
// the ID as already-processed and return nil without running fn.
// *outbox.Deduplicator satisfies this by inserting the event_id FIRST
// (INSERT ... ON CONFLICT DO NOTHING) inside a DB transaction and running fn
// only for the deliverer that actually took the row.
type Deduplicator interface {
	Process(ctx context.Context, eventID string, fn func() error) error
}

// Dispatcher consumes events, routes them to typed handlers, and manages
// retry/DLQ/dedup. Construct with NewDispatcher, register handlers with Handle,
// then call Run(ctx).
type Dispatcher struct {
	reader     Reader
	dlq        Writer
	retry      Writer
	dedup      Deduplicator
	maxRetries int
	log        *zap.Logger
	handlers   map[string]handlerFn
}

// DispatcherOption configures optional dispatcher fields.
type DispatcherOption func(*Dispatcher)

// WithRetry sets a retry topic writer. When set, transient handler errors
// below maxRetries are written to this topic with incremented x-retry-count.
func WithRetry(w Writer) DispatcherOption {
	return func(d *Dispatcher) { d.retry = w }
}

// WithDedup sets a deduplicator. Messages whose event_id has already been
// processed are skipped (offset committed, handler not invoked).
func WithDedup(dedup Deduplicator) DispatcherOption {
	return func(d *Dispatcher) { d.dedup = dedup }
}

// WithMaxRetries overrides the default retry budget (3).
func WithMaxRetries(n int) DispatcherOption {
	return func(d *Dispatcher) { d.maxRetries = n }
}

// WithLogger supplies a zap logger. Default: zap.NewNop().
func WithLogger(l *zap.Logger) DispatcherOption {
	return func(d *Dispatcher) { d.log = l }
}

// NewDispatcher constructs a Dispatcher. reader and dlq are required — no code
// path drops a failed message on the floor: a failed message is forwarded to
// the retry topic (when WithRetry is set and budget remains) or otherwise
// dead-lettered, and if that forwarding write fails the source offset is left
// uncommitted so Kafka redelivers.
func NewDispatcher(reader Reader, dlq Writer, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		reader:     reader,
		dlq:        dlq,
		maxRetries: 3,
		log:        zap.NewNop(),
		handlers:   make(map[string]handlerFn),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

type handlerFn func(ctx context.Context, rawPayload []byte, headers envelope.Headers) error

// Handle registers a typed handler. The message is routed when its event_type
// header equals proto.MessageName(*new(T)).
func Handle[T proto.Message](d *Dispatcher, fn func(context.Context, T) error) {
	var zero T
	fqn := string(proto.MessageName(zero))
	if fqn == "" {
		panic("events.Handle: proto.MessageName returned empty (nil T?)")
	}
	d.handlers[fqn] = func(ctx context.Context, raw []byte, _ envelope.Headers) error {
		msg := zero.ProtoReflect().New().Interface().(T)
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, msg); err != nil {
			return fmt.Errorf("%w: %v", errUnmarshal, err)
		}
		return fn(ctx, msg)
	}
}

// Internal sentinels for failure classification.
var (
	errUnmarshal = errors.New("unmarshal")
	errPanic     = errors.New("panic")
)

// dlqReason categorizes a handler failure for the x-dlq-reason header.
type dlqReason string

const (
	reasonMaxRetries   dlqReason = "max_retries"
	reasonPoisonPill   dlqReason = "poison_pill"
	reasonUnmarshal    dlqReason = "unmarshal_error"
	reasonHandlerPanic dlqReason = "handler_panic"
)

// Run polls the reader until ctx is cancelled. Each message is routed to its
// registered handler; on error, retry/DLQ rules apply. Returns ctx.Err() on cancel.
func (d *Dispatcher) Run(ctx context.Context) error {
	for {
		msg, err := d.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			d.log.Warn("fetch error", zap.Error(err))
			continue
		}
		d.handleOne(ctx, msg)
	}
}

func (d *Dispatcher) handleOne(ctx context.Context, msg kafka.Message) {
	h := envelope.FromKafka(msg.Headers)
	fqn := h.EventType()

	handler, ok := d.handlers[fqn]
	if !ok {
		d.log.Debug("no handler for event type; skipping",
			zap.String("event_type", fqn),
			zap.String("event_id", h.EventID()))
		_ = d.reader.CommitMessages(ctx, msg)
		return
	}

	// Attach envelope headers to the context so handlers can read fields that
	// don't appear in the proto payload (occurred_at, retry-count, ...). The
	// typed handler signature is func(ctx, T) error — handlers fish the
	// headers out via envelope.HeadersFromContext when they need them.
	ctx = envelope.WithHeaders(ctx, h)

	// Invoke through the deduplicator when one is configured. The handler runs
	// inside the same atomic unit that checks+records the event_id — so two
	// concurrent deliveries of the same message across pods can't both run the
	// handler. If dedup is not configured, run the handler directly.
	var err error
	if d.dedup != nil {
		err = d.dedup.Process(ctx, h.EventID(), func() error {
			return d.safelyInvoke(ctx, handler, msg.Value, h)
		})
	} else {
		err = d.safelyInvoke(ctx, handler, msg.Value, h)
	}
	if err == nil {
		_ = d.reader.CommitMessages(ctx, msg)
		return
	}

	reason := classify(err)
	// If the failure-path write (retry or DLQ) itself fails, do NOT commit the
	// offset — leave the message uncommitted so Kafka redelivers it instead of
	// losing it. The success path above commits unchanged.
	if werr := d.routeFailure(ctx, msg, h, err, reason); werr != nil {
		d.log.Error("failure-path write failed; leaving offset uncommitted for redelivery",
			zap.String("event_id", h.EventID()),
			zap.String("dlq_reason", string(reason)),
			zap.Error(werr))
		return
	}
	_ = d.reader.CommitMessages(ctx, msg)
}

func (d *Dispatcher) safelyInvoke(ctx context.Context, h handlerFn, raw []byte, hdr envelope.Headers) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errPanic, r)
		}
	}()
	return h(ctx, raw, hdr)
}

func classify(err error) dlqReason {
	switch {
	case errors.Is(err, ErrPoisonPill):
		return reasonPoisonPill
	case errors.Is(err, errUnmarshal):
		return reasonUnmarshal
	case errors.Is(err, errPanic):
		return reasonHandlerPanic
	default:
		return reasonMaxRetries
	}
}

// routeFailure forwards a failed message to its retry or DLQ destination and
// returns the write error, if any. A non-nil return tells handleOne to skip the
// offset commit so Kafka redelivers the message rather than losing it.
//
// Poison/unmarshal/panic failures (non-maxRetries reasons) are never retried —
// they go straight to the DLQ. For retryable (reasonMaxRetries) failures: if a
// retry writer is configured and the budget is not exhausted, the message is
// forwarded to the retry topic with an incremented x-retry-count; otherwise it
// goes to the DLQ. There is no path that drops a failed message on the floor —
// every failure is either retried or dead-lettered.
func (d *Dispatcher) routeFailure(ctx context.Context, msg kafka.Message, h envelope.Headers, err error, reason dlqReason) error {
	if reason != reasonMaxRetries {
		return d.writeDLQ(ctx, msg, err, reason)
	}
	retryCount := h.RetryCount()
	if retryCount >= d.maxRetries || d.retry == nil {
		// Budget exhausted, or no retry writer configured: dead-letter instead
		// of dropping. DLQ topics exist for every consumer group.
		return d.writeDLQ(ctx, msg, err, reasonMaxRetries)
	}
	next := kafkaCloneWithHeader(msg, envelope.HeaderRetryCount, fmt.Sprintf("%d", retryCount+1))
	if werr := d.retry.WriteMessages(ctx, next); werr != nil {
		d.log.Error("retry write failed",
			zap.String("event_id", h.EventID()),
			zap.Error(werr))
		return werr
	}
	return nil
}

// writeDLQ publishes the failed message to the DLQ topic with diagnostic
// headers and returns the write error, if any. A non-nil return propagates up
// to handleOne so the source offset is left uncommitted for redelivery.
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
			zap.Error(werr))
		return werr
	}
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

