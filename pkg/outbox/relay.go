package outbox

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/kafkawriter"
)

// RelayConfig tunes the polling relay behavior.
type RelayConfig struct {
	PollInterval time.Duration // How often to poll for pending messages (default: 1s)
	BatchSize    int           // Max messages per TOPIC per poll cycle (default: 100)

	// ShutdownFlushTimeout bounds how long an already-claimed batch may keep
	// writing to Kafka and marking itself sent after the relay's context is
	// cancelled (default: DefaultShutdownFlushTimeout). Zero or negative means
	// the default, so a hand-built RelayConfig cannot turn it into "abandon
	// every in-flight batch".
	ShutdownFlushTimeout time.Duration

	// SendTimeout bounds one topic's Kafka write while its rows are locked
	// (default: DefaultSendTimeout). A write that outlasts it fails, the
	// transaction rolls back and the rows are sent again on a later poll: a hung
	// broker connection can never hold a topic's rows hostage.
	SendTimeout time.Duration

	// TopicConcurrency is how many topics the relay works at once, each on its
	// own pooled connection while it writes (default: DefaultTopicConcurrency).
	// It is what lets healthy topics keep flowing while a stuck one waits out
	// its Kafka timeouts.
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
	// retried every poll, and a line per second is noise, not signal: the
	// by-topic backlog gauges are what alerts.
	stuckTopicLogEvery = 30 * time.Second
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

// Relay forwards pending outbox_messages rows to Kafka, one topic at a time.
//
// Each topic is one transaction: claim its oldest rows (FOR UPDATE SKIP LOCKED),
// write them to Kafka, mark what was acknowledged, commit (Store.ForwardTopic).
// Two relays never send the same rows; a crash before the commit re-sends, and
// the consumer's deduplicator absorbs the repeat. A topic that cannot be written
// waits, in order, while the other topics keep flowing.
//
// TODO: CDC (Debezium) relay for real-time propagation.
type Relay struct {
	store  relayStore
	writer relayWriter
	logger *zap.Logger
	cfg    RelayConfig

	logMu      sync.Mutex
	lastLogged map[string]time.Time
}

// NewRelay creates a new polling relay. The concrete parameter types are kept
// so every existing call site compiles unchanged; the fields behind them are
// interfaces purely for testability.
//
// It panics when writer is Async or RequireNone (kafkawriter.Check): the relay
// marks a row sent as soon as WriteMessages returns nil, which such a writer
// does before any broker answered.
func NewRelay(store *Store, writer *kafka.Writer, logger *zap.Logger, cfg RelayConfig) *Relay {
	kafkawriter.MustCheck("outbox.NewRelay", writer)
	return &Relay{
		store:  store,
		writer: writer,
		logger: logger,
		cfg:    cfg,
	}
}

// Run starts the relay loop. Blocks until ctx is cancelled.
// When a topic returned a full batch it immediately polls again to drain the
// backlog; otherwise it sleeps for PollInterval before the next poll.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		zap.Duration("poll_interval", r.pollInterval()),
		zap.Int("batch_size", r.batchSize()),
		zap.Int("topic_concurrency", r.topicConcurrency()),
		zap.Duration("send_timeout", r.sendTimeout()),
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

		res := r.poll(ctx)
		if res.failed {
			recordRelayError()
		} else {
			// Error-free means successful, whether or not any row was found.
			recordRelaySuccess(0)
		}
		recordRelayed(res.sent)

		// A topic gave a full batch and wrote it cleanly: more may be waiting.
		if res.more {
			continue
		}

		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return ctx.Err()
		case <-time.After(r.pollInterval()):
		}
	}
}

// pollResult is the outcome of one poll over every pending topic.
type pollResult struct {
	sent   int  // rows marked sent across all topics
	failed bool // the topic list, a claim, a write or a mark failed
	more   bool // a topic returned a full batch and wrote it cleanly
}

// poll works every pending topic once, up to TopicConcurrency at a time.
//
// The topic list is read on ctx, so no new work starts once the relay is
// cancelled. A topic already started is finished on a flush context: its rows
// are written AND marked sent even if a shutdown arrives mid-batch — abandoning
// it there would leave rows Kafka already holds pending, to be re-published on
// the next start.
func (r *Relay) poll(ctx context.Context) pollResult {
	topics, err := r.store.PendingTopics(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Error("outbox relay: list pending topics failed", zap.Error(err))
		}
		return pollResult{failed: ctx.Err() == nil}
	}

	var (
		mu  sync.Mutex
		out pollResult
		wg  sync.WaitGroup
		sem = make(chan struct{}, r.topicConcurrency())
	)
	for _, topic := range topics {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(topic string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := r.forwardTopic(ctx, topic)

			mu.Lock()
			defer mu.Unlock()
			out.sent += res.Sent
			if err != nil {
				out.failed = true
			} else if res.Claimed >= r.batchSize() && res.Sent == res.Claimed {
				out.more = true
			}
		}(topic)
	}
	wg.Wait()
	return out
}

// forwardTopic claims, writes and marks one topic's rows, and accounts for the
// outcome. The error is returned only so poll can see the poll was not clean.
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
func (r *Relay) sendFunc() SendFunc {
	return func(ctx context.Context, msgs []*Message) ([]string, error) {
		sendCtx, cancel := context.WithTimeout(ctx, r.sendTimeout())
		defer cancel()

		kafkaMsgs := make([]kafka.Message, len(msgs))
		for i, msg := range msgs {
			kafkaMsgs[i] = toKafkaMessage(msg)
		}
		err := r.writer.WriteMessages(sendCtx, kafkaMsgs...)
		return deliveredIDs(msgs, err), err
	}
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

// logFailure reports a topic that could not be forwarded: the first time and
// then every stuckTopicLogEvery, so a topic that stays stuck does not write a
// line per poll.
func (r *Relay) logFailure(topic string, res ForwardResult, err error) {
	r.logMu.Lock()
	if r.lastLogged == nil {
		r.lastLogged = make(map[string]time.Time)
	}
	last, seen := r.lastLogged[topic]
	loud := !seen || time.Since(last) >= stuckTopicLogEvery
	if loud {
		r.lastLogged[topic] = time.Now()
	}
	r.logMu.Unlock()

	fields := []zap.Field{
		zap.String("topic", topic),
		zap.Int("claimed", res.Claimed),
		zap.Int("sent", res.Sent),
		zap.Error(err),
	}
	if loud {
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

func (r *Relay) topicConcurrency() int {
	if r.cfg.TopicConcurrency > 0 {
		return r.cfg.TopicConcurrency
	}
	return DefaultTopicConcurrency
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
