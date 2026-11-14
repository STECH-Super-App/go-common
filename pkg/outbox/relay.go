package outbox

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
	"github.com/STECH-Super-App/go-common/pkg/kafkawriter"
)

// RelayConfig tunes the polling relay behavior.
type RelayConfig struct {
	PollInterval time.Duration // How often to look for pending topics (default: 1s)
	BatchSize    int           // Max messages per TOPIC per forward (default: 100)

	// ShutdownFlushTimeout bounds how long an already-claimed batch may keep
	// writing to Kafka and marking itself sent after the relay's context is
	// cancelled (default: DefaultShutdownFlushTimeout). Zero or negative means
	// the default, so a hand-built RelayConfig cannot turn it into "abandon
	// every in-flight batch".
	ShutdownFlushTimeout time.Duration

	// SendTimeout bounds one topic's Kafka write while its rows are locked
	// (default: DefaultSendTimeout). A write that outlasts it fails, the
	// transaction rolls back and the rows are sent again after the topic's
	// backoff: a hung broker connection can never hold a topic's rows hostage.
	SendTimeout time.Duration

	// TopicConcurrency is how many topics the relay writes at once, each on its
	// own pooled connection while it writes (default: DefaultTopicConcurrency).
	// It is capped to a quarter of the connection pool (see Relay), so a Kafka
	// outage cannot pin the service's database connections. A topic that has
	// failed may use at most TopicConcurrency-1 of the slots, so one is always
	// left for a topic that has not.
	TopicConcurrency int
}

// Relay defaults. A zero or negative value means the default: BatchSize 0 used
// to be LIMIT 0 plus a `continue` without sleeping, a hot loop against Postgres
// that forwarded nothing (SL-15).
const (
	DefaultPollInterval = time.Second
	DefaultBatchSize    = 100

	// DefaultShutdownFlushTimeout is RelayConfig.ShutdownFlushTimeout's default.
	DefaultShutdownFlushTimeout = 5 * time.Second

	// DefaultSendTimeout is RelayConfig.SendTimeout's default. The Kafka writer
	// retries five times with up to one second between attempts, so a healthy
	// broker answers or refuses well inside it.
	DefaultSendTimeout = 30 * time.Second

	// DefaultTopicConcurrency is RelayConfig.TopicConcurrency's default.
	DefaultTopicConcurrency = 4

	// stuckTopicLogEvery is how often the relay repeats the Error line for a
	// topic that keeps failing; in between it logs at Debug. A stuck topic is
	// retried on its backoff, and a line per attempt is noise, not signal: the
	// by-topic backlog gauges are what alerts.
	stuckTopicLogEvery = 30 * time.Second

	// topicBackoffCap is the longest a failing topic waits between attempts. The
	// wait starts at one PollInterval and doubles.
	topicBackoffCap = 30 * time.Second

	// poolShare: the relay may hold at most 1/poolShare of the pool's connections.
	poolShare = 4

	// kafka-go's own defaults for a Writer whose BatchSize / BatchBytes are zero.
	kafkaDefaultBatchSize  = 100
	kafkaDefaultBatchBytes = 1 << 20

	// chunkBytesShare is the share of the writer's BatchBytes a chunk may fill by
	// the relay's estimate of its size; the rest is margin for the framing the
	// estimate does not model.
	chunkBytesShare = 0.9
	// recordOverhead and headerOverhead over-estimate the per-record and
	// per-header framing kafka-go adds (length prefixes, crc, timestamp).
	recordOverhead = 40
	headerOverhead = 10
)

// relayStore is the slice of *Store the relay actually uses. It exists so the
// poll loop — and the metric wiring hanging off its outcomes — can be unit
// tested without Postgres. *Store satisfies it.
type relayStore interface {
	PendingTopics(ctx context.Context) ([]string, error)
	ForwardTopic(ctx context.Context, topic string, limit int, send SendFunc) (ForwardResult, error)
}

// relayWriter is the slice of *kafka.Writer the relay actually uses, for the
// same reason. *kafka.Writer satisfies it.
type relayWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Relay forwards pending outbox_messages rows to Kafka, each topic on its own.
//
// Each forward is one transaction on one topic: claim its oldest rows (FOR
// UPDATE SKIP LOCKED), write them to Kafka, mark what was acknowledged, commit
// (Store.ForwardTopic). A relay never waits for a poll to finish: Run looks at
// the pending topics every PollInterval and starts a forward for every topic
// that is not already running and not backing off, so a topic blocked for
// SendTimeout delays nobody else, and a topic that fails waits out its own
// exponential backoff (1 poll, 2, 4 ... 30 s) without holding a slot.
//
// Two relays never claim the same rows at the same time (a per-topic advisory
// lock); a crash — or a timed-out write that kafka-go still completes — can make
// a row be sent twice, and the consumer's deduplicator absorbs the repeat. A
// topic that cannot be written waits, in order, while the other topics flow.
//
// TODO: CDC (Debezium) relay for real-time propagation.
type Relay struct {
	store  relayStore
	writer relayWriter
	logger *zap.Logger
	cfg    RelayConfig

	// The limits of one kafka-go batch of the writer: a chunk handed to one
	// WriteMessages call must fit one, or kafka-go splits it into several
	// batches per partition and a failed earlier batch does not stop a later one.
	// Zero means kafka-go's default.
	writerBatchSize  int
	writerBatchBytes int64

	// poolCap is the most topics the relay may write at once without taking more
	// than a quarter of the pool (zero: no cap known).
	poolCap int

	logMu      sync.Mutex
	lastLogged map[string]time.Time

	mu      sync.Mutex // guards topics, running, runningSuspect
	topics  map[string]*topicState
	running int
	// runningSuspect counts running topics that have failed before.
	runningSuspect int
	wg             sync.WaitGroup
}

// topicState is what the relay remembers of one topic between polls.
type topicState struct {
	inFlight  bool
	failures  int       // consecutive failed forwards
	retryAt   time.Time // not before: the topic's backoff
	busySince time.Time // first of an unbroken run of Busy answers
}

// topicOutcome is the result of one forward of one topic.
type topicOutcome struct {
	topic string
	res   ForwardResult
	err   error
	more  bool // a full batch was written cleanly: more may be waiting
}

// NewRelay creates a new polling relay. The concrete parameter types are kept
// so every existing call site compiles unchanged; the fields behind them are
// interfaces purely for testability.
//
// It panics when writer is Async or RequireNone, or does not partition by key
// (kafkawriter.CheckProducer): the relay marks a row sent as soon as
// WriteMessages returns nil, which such a writer does before any broker
// answered, and a domain event's key must keep its partition.
func NewRelay(store *Store, writer *kafka.Writer, logger *zap.Logger, cfg RelayConfig) *Relay {
	kafkawriter.MustCheckProducer("outbox.NewRelay", writer)
	r := &Relay{
		store:            store,
		writer:           writer,
		logger:           logger,
		cfg:              cfg,
		writerBatchSize:  writer.BatchSize,
		writerBatchBytes: writer.BatchBytes,
	}
	if store != nil && store.pool != nil {
		r.poolCap = max(1, int(store.pool.Config().MaxConns)/poolShare)
		if want := r.configuredConcurrency(); want > r.poolCap {
			logger.Warn("outbox relay: topic concurrency capped to a quarter of the connection pool",
				zap.Int("configured", want), zap.Int("effective", r.poolCap),
				zap.Int32("pool_max_conns", store.pool.Config().MaxConns))
		}
	}
	return r
}

// Run starts the relay loop. Blocks until ctx is cancelled, then waits for the
// forwards already running (each bounded by ShutdownFlushTimeout after the
// cancel). When a topic returned a full batch it looks again at once to drain
// the backlog; otherwise it sleeps for PollInterval.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		zap.Duration("poll_interval", r.pollInterval()),
		zap.Int("batch_size", r.batchSize()),
		zap.Int("topic_concurrency", r.topicConcurrency()),
		zap.Int("topic_concurrency_configured", r.configuredConcurrency()),
		zap.Duration("send_timeout", r.sendTimeout()),
		zap.Duration("shutdown_flush_timeout", r.shutdownFlushTimeout()),
	)

	// Seed the freshness gauge at start: an unset Prometheus gauge reads 0,
	// which would make the "relay stalled" alert fire on every fresh deploy
	// before the first poll completes.
	recordRelaySuccess(0)

	wake := make(chan struct{}, 1)
	report := func(o topicOutcome) {
		recordRelayed(o.res.Sent)
		switch {
		case o.err != nil:
			recordRelayError()
		case r.failingTopics() == 0:
			// A clean forward while no topic is failing refreshes freshness.
			recordRelaySuccess(0)
		}
		if o.more {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}

	for {
		if ctx.Err() == nil {
			started, err := r.tick(ctx, report)
			switch {
			case err != nil:
				if ctx.Err() == nil {
					recordRelayError()
				}
			case started == 0 && r.failingTopics() == 0:
				// Nothing to start and nothing failing: an idle, healthy poll.
				recordRelaySuccess(0)
			}
		}

		select {
		case <-ctx.Done():
			r.wg.Wait()
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		case <-wake:
		case <-time.After(r.pollInterval()):
		}
	}
}

// tick lists the pending topics and starts a forward for each one that is not
// running, not backing off and fits the concurrency limits. It does not wait for
// them: each reports its outcome to report when it ends. Healthy topics are
// offered the slots first, and a topic that has failed may take all but one of
// them, so a handful of stuck topics cannot starve the healthy ones.
//
// The topic list is read on ctx, so no new work starts once the relay is
// cancelled. A forward already started is finished on a flush context (see
// forwardTopic).
func (r *Relay) tick(ctx context.Context, report func(topicOutcome)) (started int, err error) {
	topics, err := r.store.PendingTopics(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Error("outbox relay: list pending topics failed", zap.Error(err))
		}
		return 0, err
	}

	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.topics == nil {
		r.topics = make(map[string]*topicState)
	}
	// A topic with no pending rows has nothing to back off from.
	listed := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		listed[t] = struct{}{}
	}
	for t, st := range r.topics {
		if _, ok := listed[t]; !ok && !st.inFlight {
			delete(r.topics, t)
		}
	}

	for _, suspectPass := range []bool{false, true} {
		for _, topic := range topics {
			if ctx.Err() != nil {
				return started, nil
			}
			st := r.topics[topic]
			if st == nil {
				st = &topicState{}
				r.topics[topic] = st
			}
			suspect := st.failures > 0
			if suspect != suspectPass || st.inFlight || now.Before(st.retryAt) || !r.hasSlot(suspect) {
				continue
			}
			st.inFlight = true
			r.running++
			if suspect {
				r.runningSuspect++
			}
			r.wg.Add(1)
			started++
			go r.work(ctx, topic, suspect, report)
		}
	}
	return started, nil
}

// hasSlot says whether one more forward may start. mu must be held.
func (r *Relay) hasSlot(suspect bool) bool {
	limit := r.topicConcurrency()
	if r.running >= limit {
		return false
	}
	if suspect {
		// Keep one slot for a topic that has not failed — unless there is only one.
		return r.runningSuspect < max(limit-1, 1)
	}
	return true
}

// work is one forward of one topic, run in its own goroutine.
func (r *Relay) work(ctx context.Context, topic string, suspect bool, report func(topicOutcome)) {
	defer r.wg.Done()
	res, err := r.forwardTopic(ctx, topic)
	out := topicOutcome{
		topic: topic, res: res, err: err,
		more: err == nil && !res.Busy && res.Claimed >= r.batchSize() && res.Sent == res.Claimed,
	}

	r.mu.Lock()
	r.running--
	if suspect {
		r.runningSuspect--
	}
	st := r.topics[topic]
	var busyFor time.Duration
	if st != nil {
		st.inFlight = false
		switch {
		case err != nil:
			st.failures++
			st.retryAt = time.Now().Add(r.backoff(st.failures))
			st.busySince = time.Time{}
		case res.Busy:
			if st.busySince.IsZero() {
				st.busySince = time.Now()
			}
			busyFor = time.Since(st.busySince)
		default:
			st.failures, st.retryAt, st.busySince = 0, time.Time{}, time.Time{}
		}
	}
	r.mu.Unlock()

	if res.Busy {
		r.noteBusy(topic, busyFor)
	}
	report(out)
}

// backoff is the wait before a topic that failed n times in a row is tried again.
func (r *Relay) backoff(failures int) time.Duration {
	d := r.pollInterval()
	for i := 1; i < failures && d < topicBackoffCap; i++ {
		d *= 2
	}
	return min(d, topicBackoffCap)
}

// failingTopics counts the topics whose last forward failed.
func (r *Relay) failingTopics() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, st := range r.topics {
		if st.failures > 0 {
			n++
		}
	}
	return n
}

// noteBusy accounts for a forward that found its topic held by another relay's
// transaction. One is routine (a rolling deploy); a topic that stays held for
// longer than any healthy holder could hold it (a forward is bounded by
// SendTimeout) is a hung relay or a stranded connection, and is logged.
func (r *Relay) noteBusy(topic string, heldFor time.Duration) {
	recordTopicBusy(topic)
	if heldFor > 2*r.sendTimeout() {
		if r.shouldLog("busy:" + topic) {
			r.logger.Warn("outbox relay: topic has been held by another relay's transaction for longer than a forward can take; its rows wait",
				zap.String("topic", topic), zap.Duration("held_for", heldFor))
		}
		return
	}
	r.logger.Debug("outbox relay: topic held by another relay, skipped", zap.String("topic", topic))
}

// forwardTopic claims, writes and marks one topic's rows, and accounts for the
// outcome. A forward already started is finished on a flush context: its rows
// are written AND marked sent even if a shutdown arrives mid-batch — abandoning
// it there would leave rows Kafka already holds pending, to be re-published on
// the next start.
func (r *Relay) forwardTopic(ctx context.Context, topic string) (ForwardResult, error) {
	flushCtx, release := r.flushContext(ctx)
	defer release()

	res, err := r.store.ForwardTopic(flushCtx, topic, r.batchSize(), r.sendFunc())
	if res.Sent > 0 {
		recordTopicRelayed(topic, res.Sent)
		r.logger.Debug("outbox relay: forwarded messages",
			zap.String("topic", topic), zap.Int("count", res.Sent))
	}
	if err != nil {
		recordTopicError(topic)
		r.logFailure(topic, res, err)
	}
	return res, err
}

// sendFunc writes one topic's claimed rows to Kafka and says which ones the
// broker acknowledged.
//
// The rows go out in chunks that each fit ONE kafka-go batch, one WriteMessages
// call per chunk. A single call holding more than the writer's BatchSize
// messages (or BatchBytes) for one partition is split by kafka-go into several
// batches sent concurrently, and a failed earlier batch does not stop a later
// one: A2 and A3 could be on the broker while A1 is not, and A1 would then be
// re-sent after them. The relay stops the topic at the first chunk with any
// failure, so what was acknowledged is always a prefix per key.
func (r *Relay) sendFunc() SendFunc {
	return func(ctx context.Context, msgs []*Message) ([]string, error) {
		sendCtx, cancel := context.WithTimeout(ctx, r.sendTimeout())
		defer cancel()

		delivered := make([]string, 0, len(msgs))
		for _, chunk := range r.chunks(msgs) {
			kafkaMsgs := make([]kafka.Message, len(chunk))
			for i, msg := range chunk {
				kafkaMsgs[i] = toKafkaMessage(msg)
			}
			err := r.writer.WriteMessages(sendCtx, kafkaMsgs...)
			delivered = append(delivered, deliveredIDs(chunk, err)...)
			if err != nil {
				r.noteTooLarge(chunk, err)
				return delivered, err
			}
		}
		return delivered, nil
	}
}

// chunks splits msgs, in order, into runs that each fit one kafka-go batch: at
// most the writer's BatchSize messages and, by an over-estimate of their framed
// size, at most 90% of its BatchBytes. A message that alone is over the byte
// budget gets a chunk of its own: kafka-go sends it as a batch of one, or
// refuses it with MessageTooLargeError (which noteTooLarge names).
func (r *Relay) chunks(msgs []*Message) [][]*Message {
	maxCount := r.batchLimit()
	maxBytes := int64(float64(r.batchBytesLimit()) * chunkBytesShare)

	var out [][]*Message
	var cur []*Message
	var curBytes int64
	for _, m := range msgs {
		size := estimatedRecordSize(m)
		if len(cur) > 0 && (len(cur) >= maxCount || curBytes+size > maxBytes) {
			out = append(out, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, m)
		curBytes += size
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// estimatedRecordSize over-estimates the bytes kafka-go counts for a message
// against BatchBytes (key, value and headers with their length prefixes).
func estimatedRecordSize(m *Message) int64 {
	n := int64(recordOverhead + len(m.Key) + len(m.Payload))
	for k, v := range m.Headers {
		n += int64(len(k) + len(v) + headerOverhead)
	}
	return n
}

// noteTooLarge names the row kafka-go refused for its size. Such a row is
// never sent and holds its topic until a person removes it, so it must not be
// left to the generic failure line.
func (r *Relay) noteTooLarge(chunk []*Message, err error) {
	var tooLarge kafka.MessageTooLargeError
	if !errors.As(err, &tooLarge) {
		return
	}
	topic := tooLarge.Message.Topic
	recordTopicMessageTooLarge(topic)
	if !r.shouldLog("toolarge:" + topic) {
		return
	}
	var eventID string
	for _, h := range tooLarge.Message.Headers {
		if h.Key == envelope.HeaderEventID {
			eventID = string(h.Value)
		}
	}
	r.logger.Error("outbox relay: a message is over the Kafka writer's batch limit and can never be sent; its topic is stuck behind it until the row is removed or the limit is raised",
		zap.String("topic", topic),
		zap.String("event_id", eventID),
		zap.String("key", string(tooLarge.Message.Key)),
		zap.Int("value_bytes", len(tooLarge.Message.Value)),
		zap.Int64("batch_bytes_limit", r.batchBytesLimit()),
		zap.Int("rows_in_chunk", len(chunk)))
}

// deliveredIDs says which rows of msgs a WriteMessages result acknowledged.
//
// kafka-go returns nil (all written), kafka.WriteErrors with one entry per
// message (a partial failure: a partition without a leader, a message the
// broker refused) or one error for the whole call (broker down). A row counts
// as delivered when its entry is nil AND no earlier row with the same key
// failed: marking a later event of an aggregate sent past a failed earlier one
// would let the earlier one be re-sent AFTER it. The failed row and everything
// behind it for that key stay pending, in order.
func deliveredIDs(msgs []*Message, err error) []string {
	if err == nil {
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return ids
	}
	var werrs kafka.WriteErrors
	if !errors.As(err, &werrs) || len(werrs) != len(msgs) {
		return nil
	}
	blocked := make(map[string]struct{})
	ids := make([]string, 0, len(msgs))
	for i, werr := range werrs {
		if werr != nil {
			blocked[msgs[i].Key] = struct{}{}
			continue
		}
		if _, held := blocked[msgs[i].Key]; held {
			continue
		}
		ids = append(ids, msgs[i].ID)
	}
	return ids
}

// shouldLog rate-limits a log line per key: the first time and then every
// stuckTopicLogEvery.
func (r *Relay) shouldLog(key string) bool {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if r.lastLogged == nil {
		r.lastLogged = make(map[string]time.Time)
	}
	last, seen := r.lastLogged[key]
	if seen && time.Since(last) < stuckTopicLogEvery {
		return false
	}
	r.lastLogged[key] = time.Now()
	return true
}

// logFailure reports a topic that could not be forwarded: the first time and
// then every stuckTopicLogEvery, so a topic that stays stuck does not write a
// line per attempt.
func (r *Relay) logFailure(topic string, res ForwardResult, err error) {
	fields := []zap.Field{
		zap.String("topic", topic),
		zap.Int("claimed", res.Claimed),
		zap.Int("sent", res.Sent),
		zap.Error(err),
	}
	if r.shouldLog("fail:" + topic) {
		r.logger.Error("outbox relay: topic could not be forwarded; its rows wait in order", fields...)
		return
	}
	r.logger.Debug("outbox relay: topic still failing", fields...)
}

// pollInterval and batchSize apply the defaults for non-positive config values.
func (r *Relay) pollInterval() time.Duration {
	if r.cfg.PollInterval > 0 {
		return r.cfg.PollInterval
	}
	return DefaultPollInterval
}

func (r *Relay) sendTimeout() time.Duration {
	if r.cfg.SendTimeout > 0 {
		return r.cfg.SendTimeout
	}
	return DefaultSendTimeout
}

// configuredConcurrency is TopicConcurrency with its default applied.
func (r *Relay) configuredConcurrency() int {
	if r.cfg.TopicConcurrency > 0 {
		return r.cfg.TopicConcurrency
	}
	return DefaultTopicConcurrency
}

// topicConcurrency is how many topics are written at once: the configured
// number, never more than a quarter of the pool.
func (r *Relay) topicConcurrency() int {
	n := r.configuredConcurrency()
	if r.poolCap > 0 {
		n = min(n, r.poolCap)
	}
	return n
}

// batchLimit and batchBytesLimit are the limits of one batch of the Kafka
// writer, with kafka-go's defaults for the unset.
func (r *Relay) batchLimit() int {
	if r.writerBatchSize > 0 {
		return r.writerBatchSize
	}
	return kafkaDefaultBatchSize
}

func (r *Relay) batchBytesLimit() int64 {
	if r.writerBatchBytes > 0 {
		return r.writerBatchBytes
	}
	return kafkaDefaultBatchBytes
}

func (r *Relay) batchSize() int {
	if r.cfg.BatchSize > 0 {
		return r.cfg.BatchSize
	}
	return DefaultBatchSize
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
