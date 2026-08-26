package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
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
	// logProvided records whether WithLogger supplied a real logger. It gates
	// seeding the handler context: a Nop-derived child placed on the context
	// would be FOUND by logger.FromContext and would silently swallow every
	// handler log line, shadowing the process-logger fallback.
	logProvided bool
	group       string
	handlers    map[string]handlerFn
}

// GroupUnknown is the group label value used when WithGroup was not supplied.
// It is deliberately a real value rather than an empty label: a metric that
// silently drops its group dimension is harder to notice than one that says
// "unknown".
const GroupUnknown = "unknown"

// tracerName identifies the instrumentation library in consumer spans.
const tracerName = "github.com/STECH-Super-App/go-common/pkg/events"

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
// §3.4 consumer metric. Unset means GroupUnknown.
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
		group:      GroupUnknown,
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

	// event_id and topic are the two fields that make a consumer log line
	// searchable — the consumer-path equivalent of the request_id an HTTP line
	// carries.
	msgLog := d.log.With(
		zap.String("event_id", h.EventID()),
		zap.String("topic", msg.Topic),
	)

	handler, ok := d.handlers[fqn]
	if !ok {
		msgLog.Debug("no handler for event type; skipping",
			zap.String("event_type", fqn))
		_ = d.reader.CommitMessages(ctx, msg)
		return
	}

	// Attach envelope headers to the context so handlers can read fields that
	// don't appear in the proto payload (occurred_at, retry-count, ...). The
	// typed handler signature is func(ctx, T) error — handlers fish the
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

	// Invoke through the deduplicator when one is configured. The handler runs
	// inside the same atomic unit that checks+records the event_id — so two
	// concurrent deliveries of the same message across pods can't both run the
	// handler. If dedup is not configured, run the handler directly.
	//
	// Process returns nil both when the handler ran and when the event_id was
	// already claimed, so handlerRan is the only way to tell a real completion
	// from a deduplicated redelivery.
	handlerRan := false
	invoke := func() error {
		handlerRan = true
		return d.safelyInvoke(ctx, handler, msg.Value, h)
	}

	var err error
	if d.dedup != nil {
		err = d.dedup.Process(ctx, h.EventID(), invoke)
	} else {
		err = invoke()
	}

	if err == nil {
		if handlerRan {
			processedTotal.WithLabelValues(msg.Topic, d.group).Inc()
		} else {
			dedupHitsTotal.WithLabelValues(msg.Topic, d.group).Inc()
		}
		_ = d.reader.CommitMessages(ctx, msg)
		return
	}

	span.RecordError(err)
	handlerFailuresTotal.WithLabelValues(msg.Topic, d.group).Inc()

	reason := classify(err)
	// If the failure-path write (retry or DLQ) itself fails, do NOT commit the
	// offset — leave the message uncommitted so Kafka redelivers it instead of
	// losing it. The success path above commits unchanged.
	if werr := d.routeFailure(ctx, msg, h, err, reason); werr != nil {
		failurePathErrorsTotal.WithLabelValues(msg.Topic, d.group).Inc()
		msgLog.Error("failure-path write failed; leaving offset uncommitted for redelivery",
			zap.String("dlq_reason", string(reason)),
			zap.Error(werr))
		return
	}
	_ = d.reader.CommitMessages(ctx, msg)
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
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(h))

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
			zap.String("topic", msg.Topic),
			zap.Error(werr))
		return werr
	}
	retriedTotal.WithLabelValues(msg.Topic, d.group).Inc()
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
