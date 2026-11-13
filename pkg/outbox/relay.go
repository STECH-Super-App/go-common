package outbox

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// RelayConfig tunes the polling relay behavior.
type RelayConfig struct {
	PollInterval time.Duration // How often to poll for pending messages (default: 1s)
	BatchSize    int           // Max messages per poll cycle (default: 100)

	// ShutdownFlushTimeout bounds how long an already-fetched batch may keep
	// writing to Kafka and marking itself sent after the relay's context is
	// cancelled (default: DefaultShutdownFlushTimeout). Zero or negative means
	// the default, so a hand-built RelayConfig cannot turn it into "abandon
	// every in-flight batch".
	ShutdownFlushTimeout time.Duration

	// ParkAfterFailures is how many consecutive row-attributable write
	// failures park a row (default: DefaultParkAfterFailures). A parked row is
	// skipped by every later fetch of this process, so one unpublishable row
	// can no longer hold back the rows behind it. Zero or negative means the
	// default — parking cannot be switched off, because "off" is the wedge.
	ParkAfterFailures int
}

// DefaultShutdownFlushTimeout is RelayConfig.ShutdownFlushTimeout's default.
const DefaultShutdownFlushTimeout = 5 * time.Second

// DefaultParkAfterFailures is RelayConfig.ParkAfterFailures' default.
const DefaultParkAfterFailures = 5

// MaxParkedRows bounds the relay's parked set. Every parked id rides on every
// later fetch (`id <> ALL($2)`), so a cause that poisons EVERY row — say the
// only topic a service publishes to is missing — would otherwise park each
// row it ever writes and grow that list without limit. At the cap a row that
// reaches ParkAfterFailures is not parked; it is retried on every poll, the
// behaviour from before parking existed, until a restart clears the set.
const MaxParkedRows = 1000

// relayStore is the slice of *Store the relay actually uses. It exists so the
// poll loop — and the metric wiring hanging off its outcomes — can be unit
// tested without Postgres. *Store satisfies it.
type relayStore interface {
	FetchPendingExcept(ctx context.Context, batchSize int, exclude []string) ([]*Message, error)
	MarkSentBatch(ctx context.Context, ids []string) error
}

// relayWriter is the slice of *kafka.Writer the relay actually uses, for the
// same reason. *kafka.Writer satisfies it.
type relayWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Relay polls the outbox_messages table and forwards pending messages to Kafka.
// It uses SELECT … FOR UPDATE SKIP LOCKED for zero-contention concurrent access.
//
// Poison rows. A row Kafka will never accept — a topic the broker does not
// have, a payload over the size limit — fails kafka-go's WHOLE WriteMessages
// call, not just its own entry, and FetchPending returns the same oldest rows
// on every poll. Before the per-row fallback that one row stopped its whole
// database. Now a batch-level failure is retried row by row, and a row whose
// own write fails ParkAfterFailures times in a row is PARKED: held in this
// process's memory and excluded from every later fetch. Parking needs no
// migration (this package's table lives in every service's database) and
// changes nothing in the row — it stays 'pending', so
// outbox_oldest_pending_age_seconds keeps growing and the stall alert pages,
// while outbox_parked_events_total says why. A restart forgets the parked set
// and retries those rows, which is also how an operator retries them once the
// cause (say, the missing topic) is fixed.
//
// failures and parked are touched only by the goroutine running Run.
//
// TODO: CDC (Debezium) relay for real-time propagation.
type Relay struct {
	store  relayStore
	writer relayWriter
	logger *zap.Logger
	cfg    RelayConfig

	// failures counts each row's consecutive row-attributable write failures.
	// It only ever holds rows of the most recent batch (see retainFailures),
	// so it is bounded by BatchSize.
	failures map[string]int
	// parked holds the ids of rows this process no longer fetches. It grows
	// only by parking, i.e. by ParkAfterFailures failed writes per row, is
	// capped at MaxParkedRows, and a restart empties it.
	parked map[string]struct{}
}

// NewRelay creates a new polling relay. The concrete parameter types are kept
// so every existing call site compiles unchanged; the fields behind them are
// interfaces purely for testability.
func NewRelay(store *Store, writer *kafka.Writer, logger *zap.Logger, cfg RelayConfig) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: logger,
		cfg:    cfg,
	}
}

// Run starts the relay loop. Blocks until ctx is cancelled.
// When a full batch is fetched, it immediately polls again to drain backlogs.
// When fewer messages are returned, it sleeps for PollInterval before the next poll.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		zap.Duration("poll_interval", r.cfg.PollInterval),
		zap.Int("batch_size", r.cfg.BatchSize),
		zap.Duration("shutdown_flush_timeout", r.shutdownFlushTimeout()),
	)

	// Seed the freshness gauge at start: an unset Prometheus gauge reads 0,
	// which would make the "relay stalled" alert fire on every fresh deploy
	// before the first poll completes.
	recordRelaySuccess(0)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		default:
		}

		processed, err := r.pollAndForward(ctx)
		if err != nil {
			recordRelayError()
			r.logger.Error("outbox relay poll error", zap.Error(err))
		} else {
			// Error-free means successful, whether or not any row was found.
			recordRelaySuccess(processed)
		}

		// If we got a full batch, there might be more — poll immediately.
		if processed >= r.cfg.BatchSize {
			continue
		}

		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

// pollAndForward fetches pending messages, writes them to Kafka in a single
// batched call, and marks them all sent in a single UPDATE.
//
// Calling kafka.Writer.WriteMessages once per message blocks each call by the
// writer's BatchTimeout (1s default), which caps throughput at ~1 msg/s
// regardless of the configured BatchSize. Sending the whole slice in one call
// lets kafka-go batch them naturally and returns sub-second per poll for any
// realistic batch.
//
// The fetch runs on ctx, so no new batch is taken once the relay is
// cancelled. Everything after it runs on flushCtx: a batch that was already
// fetched is written AND marked sent even if a shutdown arrives mid-batch —
// abandoning it there would leave rows Kafka already holds pending, to be
// re-published on the next start.
//
// A batch-level failure (any error that is not kafka.WriteErrors) that does
// not look broker-wide falls back to forwardRowByRow, which is where poison
// rows are found and parked. Parked rows are excluded from the fetch itself,
// so they never take a slot of the batch.
//
// Returns the number of messages successfully processed.
func (r *Relay) pollAndForward(ctx context.Context) (int, error) {
	messages, err := r.store.FetchPendingExcept(ctx, r.cfg.BatchSize, r.parkedIDs())
	if err != nil {
		return 0, err
	}
	r.retainFailures(messages)
	if len(messages) == 0 {
		return 0, nil
	}

	flushCtx, release := r.flushContext(ctx)
	defer release()

	kafkaMsgs := make([]kafka.Message, len(messages))
	ids := make([]string, len(messages))
	for i, msg := range messages {
		kafkaMsgs[i] = toKafkaMessage(msg)
		ids[i] = msg.ID
	}

	if err := r.writer.WriteMessages(flushCtx, kafkaMsgs...); err != nil {
		// kafka-go returns either a single error (whole batch failed,
		// e.g., broker down) or kafka.WriteErrors with one entry per
		// message (partial failure). Mark only the entries that
		// succeeded; the rest stay pending and retry on the next poll.
		var werrs kafka.WriteErrors
		if errors.As(err, &werrs) {
			succeededIDs := make([]string, 0, len(messages))
			failed := 0
			for i, werr := range werrs {
				if werr == nil {
					succeededIDs = append(succeededIDs, ids[i])
					continue
				}
				failed++
				r.logger.Error("outbox relay: kafka write failed",
					zap.String("message_id", messages[i].ID),
					zap.String("event_type", messages[i].EventType),
					zap.String("topic", messages[i].Topic),
					zap.Error(werr),
				)
			}
			if len(succeededIDs) == 0 {
				// EVERY message in the batch failed. Propagate the error
				// instead of reporting an error-free poll: returning nil here
				// would refresh outbox_last_success_timestamp_seconds and leave
				// outbox_relay_errors_total flat while zero messages were
				// delivered — a wedged relay that looks perfectly healthy on
				// every panel. That is exactly the shape a missing Kafka topic
				// produces (Critical Rule 13 — Kafka topic naming and
				// provisioning), which is a live condition in prod.
				//
				// Return semantics for the caller are unchanged: the rows stay
				// pending and the next poll retries them, as before.
				return 0, err
			}
			if failed > 0 {
				// Partial delivery. The succeeded half is real progress and is
				// recorded by the caller off the nil error; the failed half is
				// a genuine error and must move the error counter, or a relay
				// losing a steady fraction of every batch reports as clean.
				recordRelayError()
			}
			if err := r.store.MarkSentBatch(flushCtx, succeededIDs); err != nil {
				r.logger.Error("outbox relay: mark sent batch failed",
					zap.Int("count", len(succeededIDs)),
					zap.Error(err),
				)
				return 0, err
			}
			r.logger.Debug("outbox relay: partial forward",
				zap.Int("succeeded", len(succeededIDs)),
				zap.Int("failed", failed),
			)
			return len(succeededIDs), nil
		}

		r.logger.Error("outbox relay: kafka batch write failed",
			zap.Int("batch_size", len(messages)),
			zap.Error(err),
		)

		// A broker that is down, a closed writer or a shutdown that cut the
		// write short says nothing about any one row: writing the rows one by
		// one would only multiply the same timeout by the batch size. Keep the
		// old behaviour — the whole batch stays pending for the next poll.
		if flushCtx.Err() != nil || classifyRowFailure(err) == failureBrokerWide {
			return 0, err
		}
		return r.forwardRowByRow(ctx, flushCtx, messages, err)
	}

	if err := r.store.MarkSentBatch(flushCtx, ids); err != nil {
		r.logger.Error("outbox relay: mark sent batch failed",
			zap.Int("count", len(ids)),
			zap.Error(err),
		)
		return 0, err
	}

	r.logger.Debug("outbox relay: forwarded messages",
		zap.Int("count", len(messages)),
	)
	return len(messages), nil
}

// rowFailure is one row whose own write failed during forwardRowByRow.
type rowFailure struct {
	msg   *Message
	err   error
	class rowFailureClass
}

// forwardRowByRow is the fallback for a batch whose WriteMessages call failed
// as a whole. It writes the rows one at a time, in fetch order, so the rows
// Kafka accepts are delivered and marked sent, and the row that broke the
// batch is identified by its own error.
//
// Rules of a pass:
//
//   - Per-key order is kept. Once a row with a non-empty Kafka key fails, the
//     later rows of the same topic and key are not attempted in this pass:
//     delivering them would put a consumer's events for that aggregate out of
//     order. They stay pending and are attempted again next poll — or flow
//     once the failing row is parked, at which point that row is lost to the
//     stream until a restart anyway.
//   - A broker-wide error (see classifyRowFailure) ends the pass: the broker
//     is gone, and each further row would only wait out the same timeout.
//   - A shutdown ends the pass between rows: no new row is written after the
//     relay's ctx is cancelled, and the rows already delivered are marked
//     sent on flushCtx, inside the ShutdownFlushTimeout grace.
//   - A row's failure counts toward parking only when it is attributable to
//     that row (see countsTowardParking); a broker-wide condition must never
//     park healthy rows.
//
// Cost: kafka-go holds a single-message write for up to the writer's
// BatchTimeout (1s by default), so a pass over a full batch is roughly
// BatchSize × BatchTimeout. That is paid only while an unparked poison row
// sits in the batch — at most ParkAfterFailures passes per such row — and is
// the price of telling one row from another. The normal path is unchanged.
//
// Return values follow the kafka.WriteErrors arm: some row delivered → the
// delivered count and a nil error (the error counter is moved here for the
// failed rows); nothing delivered → 0 and an error, so the poll counts as
// failed and the freshness gauge does not move.
func (r *Relay) forwardRowByRow(ctx, flushCtx context.Context, messages []*Message, batchErr error) (int, error) {
	succeededIDs := make([]string, 0, len(messages))
	var failed []rowFailure
	held := make(map[string]struct{})
	var stopErr error

	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			stopErr = err
			break
		}
		key := orderingKey(msg)
		if _, isHeld := held[key]; isHeld && key != "" {
			continue
		}

		var err error
		if len(messages) == 1 {
			// The batch WAS this row: its error is already the row's own.
			err = batchErr
		} else {
			err = r.writer.WriteMessages(flushCtx, toKafkaMessage(msg))
		}
		if err == nil {
			succeededIDs = append(succeededIDs, msg.ID)
			continue
		}

		err = singleWriteError(err)
		class := classifyRowFailure(err)
		if class == failureBrokerWide {
			stopErr = err
			break
		}
		failed = append(failed, rowFailure{msg: msg, err: err, class: class})
		if key != "" {
			held[key] = struct{}{}
		}
	}

	delivered := len(succeededIDs) > 0
	for _, f := range failed {
		r.recordRowFailure(f, countsTowardParking(f.class, delivered))
	}
	if stopErr != nil && !errors.Is(stopErr, context.Canceled) {
		r.logger.Error("outbox relay: row-by-row pass stopped on a broker-wide error",
			zap.Int("delivered", len(succeededIDs)),
			zap.Error(stopErr),
		)
	}

	if !delivered {
		if stopErr != nil {
			return 0, stopErr
		}
		return 0, batchErr
	}

	if len(failed) > 0 || (stopErr != nil && !errors.Is(stopErr, context.Canceled)) {
		// Partial delivery, exactly as in the kafka.WriteErrors arm: the
		// delivered rows are progress (recorded by the caller off the nil
		// error), the rest is a genuine error.
		recordRelayError()
	}
	if err := r.store.MarkSentBatch(flushCtx, succeededIDs); err != nil {
		r.logger.Error("outbox relay: mark sent batch failed",
			zap.Int("count", len(succeededIDs)),
			zap.Error(err),
		)
		return 0, err
	}
	for _, id := range succeededIDs {
		delete(r.failures, id)
	}
	r.logger.Info("outbox relay: batch forwarded row by row",
		zap.Int("delivered", len(succeededIDs)),
		zap.Int("failed", len(failed)),
		zap.Int("batch_size", len(messages)),
	)
	return len(succeededIDs), nil
}

// recordRowFailure logs one row's failed write and, when it counts, advances
// the row's consecutive-failure count — parking the row when the count reaches
// ParkAfterFailures.
func (r *Relay) recordRowFailure(f rowFailure, counts bool) {
	fields := []zap.Field{
		zap.String("message_id", f.msg.ID),
		zap.String("event_type", f.msg.EventType),
		zap.String("topic", f.msg.Topic),
		zap.String("aggregate_type", f.msg.AggregateType),
		zap.String("aggregate_id", f.msg.AggregateID),
		zap.Error(f.err),
	}
	if !counts {
		r.logger.Warn("outbox relay: row write failed; not counted toward parking (nothing in this pass was delivered, or the error is transient)",
			fields...)
		return
	}

	if r.failures == nil {
		r.failures = make(map[string]int)
	}
	n := r.failures[f.msg.ID] + 1
	limit := r.parkAfterFailures()
	fields = append(fields, zap.Int("consecutive_failures", n), zap.Int("park_after_failures", limit))
	if n < limit {
		r.failures[f.msg.ID] = n
		r.logger.Error("outbox relay: row write failed", fields...)
		return
	}

	if len(r.parked) >= MaxParkedRows {
		r.failures[f.msg.ID] = n
		r.logger.Error("outbox relay: row would be parked but the parked set is full — it is retried on every poll until this process restarts",
			append(fields, zap.Int("parked_rows", len(r.parked)))...)
		return
	}

	delete(r.failures, f.msg.ID)
	if r.parked == nil {
		r.parked = make(map[string]struct{})
	}
	r.parked[f.msg.ID] = struct{}{}
	recordParked()
	r.logger.Error("outbox relay: row PARKED — it stays pending and is skipped until this process restarts", fields...)
}

// countsTowardParking decides whether a row's failed write may move it toward
// parking. delivered says whether any row of the same pass reached Kafka.
func countsTowardParking(class rowFailureClass, delivered bool) bool {
	switch class {
	case failureRowScoped:
		return true
	case failureUnclassified:
		// The writer delivered another row in this very pass, so the broker
		// is up and this failure belongs to this row. Without that proof an
		// unknown error (a SASL failure, say) could be fleet-wide, and
		// counting it would park every healthy row of the batch.
		return delivered
	default:
		return false
	}
}

// parkAfterFailures applies the ParkAfterFailures default.
func (r *Relay) parkAfterFailures() int {
	if r.cfg.ParkAfterFailures > 0 {
		return r.cfg.ParkAfterFailures
	}
	return DefaultParkAfterFailures
}

// parkedIDs returns the ids the next fetch must skip, or nil when none are
// parked.
func (r *Relay) parkedIDs() []string {
	if len(r.parked) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.parked))
	for id := range r.parked {
		ids = append(ids, id)
	}
	return ids
}

// retainFailures drops the failure counts of rows the latest fetch did not
// return — rows that were delivered, deleted, or taken by another relay
// replica. Dropping a count only delays a park; keeping it would let the map
// grow without bound.
func (r *Relay) retainFailures(batch []*Message) {
	if len(r.failures) == 0 {
		return
	}
	inBatch := make(map[string]struct{}, len(batch))
	for _, msg := range batch {
		inBatch[msg.ID] = struct{}{}
	}
	for id := range r.failures {
		if _, ok := inBatch[id]; !ok {
			delete(r.failures, id)
		}
	}
}

// orderingKey is the scope Kafka orders a row's key within. An empty key
// orders nothing, so it returns "" and the row is never held back.
func orderingKey(msg *Message) string {
	if msg.Key == "" {
		return ""
	}
	return msg.Topic + "\x00" + msg.Key
}

// singleWriteError unwraps the one-entry kafka.WriteErrors kafka-go returns
// for a failed single-message write, so the row's own error is classified.
func singleWriteError(err error) error {
	var werrs kafka.WriteErrors
	if errors.As(err, &werrs) && len(werrs) == 1 && werrs[0] != nil {
		return werrs[0]
	}
	return err
}

// rowFailureClass sorts a failed write by what it says about the row.
type rowFailureClass int

const (
	// failureUnclassified: nothing in the error says whose fault it is.
	failureUnclassified rowFailureClass = iota
	// failureRowScoped: the error is a property of this row, whatever the
	// broker's state.
	failureRowScoped
	// failureTransient: Kafka called the condition temporary (a leader
	// election, a request timeout); it is expected to heal by itself.
	failureTransient
	// failureBrokerWide: the broker or the writer is unusable — no row can
	// get through, and no row is to blame.
	failureBrokerWide
)

// classifyRowFailure classifies a write error, most specific first.
//
//   - Row-scoped: kafka-go's client-side size check (MessageTooLargeError,
//     raised before anything is sent), and the broker codes that describe the
//     row's topic or record — unknown/invalid topic, no authorisation for the
//     topic, message or record list too large, invalid record. kafka-go calls
//     UnknownTopicOrPartition temporary, but in the clouds topics are never
//     auto-created (Critical Rule 13), so a missing topic does not heal on its
//     own and must not hold the rows behind it hostage.
//   - Broker-wide: a cancelled or expired context, a closed writer, a dropped
//     connection (io.EOF), any other network error.
//   - Transient: every other code kafka-go marks Temporary.
//   - Unclassified: the rest.
//
// kafka.Error itself implements net.Error, so it is matched before the
// net.Error arm.
func classifyRowFailure(err error) rowFailureClass {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return failureBrokerWide
	}

	var tooLarge kafka.MessageTooLargeError
	if errors.As(err, &tooLarge) {
		return failureRowScoped
	}

	var kerr kafka.Error
	if errors.As(err, &kerr) {
		switch kerr {
		case kafka.UnknownTopicOrPartition,
			kafka.InvalidTopic,
			kafka.TopicAuthorizationFailed,
			kafka.MessageSizeTooLarge,
			kafka.RecordListTooLarge,
			kafka.InvalidRecord:
			return failureRowScoped
		}
		if kerr.Temporary() {
			return failureTransient
		}
		return failureUnclassified
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return failureBrokerWide
	}
	return failureUnclassified
}

// shutdownFlushTimeout applies the ShutdownFlushTimeout default.
func (r *Relay) shutdownFlushTimeout() time.Duration {
	if r.cfg.ShutdownFlushTimeout > 0 {
		return r.cfg.ShutdownFlushTimeout
	}
	return DefaultShutdownFlushTimeout
}

// flushContext returns the context an already-fetched batch is written and
// marked sent on: detached from ctx's cancellation (values are kept), and
// cancelled ShutdownFlushTimeout after ctx is — so a shutdown never abandons
// the batch, yet can never be held up by it for longer than the timeout.
//
// The bound starts at cancellation, not at fetch: while the relay runs
// normally the write keeps today's semantics (the Kafka writer's own
// timeouts and retries govern it), so a slow broker cannot make every batch
// fail on an arbitrary clock and re-publish itself on each poll.
//
// release must be called once the batch is done.
func (r *Relay) flushContext(ctx context.Context) (flushCtx context.Context, release func()) {
	flushCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	grace := r.shutdownFlushTimeout()
	stopWatching := context.AfterFunc(ctx, func() {
		// Firing after release is harmless: cancel is idempotent.
		time.AfterFunc(grace, cancel)
	})
	return flushCtx, func() {
		stopWatching()
		cancel()
	}
}

// toKafkaMessage converts an outbox Message to a kafka-go Message.
func toKafkaMessage(msg *Message) kafka.Message {
	headers := make([]kafka.Header, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, kafka.Header{
			Key:   k,
			Value: []byte(v),
		})
	}

	return kafka.Message{
		Topic:   msg.Topic,
		Key:     []byte(msg.Key),
		Value:   msg.Payload,
		Headers: headers,
	}
}
