package outbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/STECH-Super-App/go-common/pkg/envelope"
)

// --- doubles ---

func (s *fakeRelayStore) add(msgs ...*Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, msgs...)
}

func topicRows(topic string, n int) []*Message {
	out := make([]*Message, n)
	for i := range out {
		id := newID()
		out[i] = &Message{ID: id, Key: id, Topic: topic, EventType: "events.x.v1.X", Status: StatusPending,
			Payload: []byte(`{}`), CreatedAt: time.Now().UTC()}
	}
	return out
}

// gateWriter answers per topic: a topic in `blocked` waits until released (or
// the context ends, as a hung broker connection does until SendTimeout), a
// topic in `failing` returns an error at once. Everything else is acknowledged
// and recorded.
type gateWriter struct {
	mu      sync.Mutex
	blocked map[string]chan struct{}
	failing map[string]bool
	sent    map[string]int
	entered map[string]int
}

func newGateWriter() *gateWriter {
	return &gateWriter{blocked: map[string]chan struct{}{}, failing: map[string]bool{}, sent: map[string]int{}, entered: map[string]int{}}
}

func (w *gateWriter) block(topic string) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch := make(chan struct{})
	w.blocked[topic] = ch
	return ch
}

func (w *gateWriter) sentTo(topic string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sent[topic]
}

func (w *gateWriter) enteredCount(topic string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.entered[topic]
}

func (w *gateWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	topic := msgs[0].Topic
	w.mu.Lock()
	w.entered[topic]++
	gate, blocked := w.blocked[topic]
	failing := w.failing[topic]
	w.mu.Unlock()
	if failing {
		return errors.New("broker unavailable")
	}
	if blocked {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	w.mu.Lock()
	w.sent[topic] += len(msgs)
	w.mu.Unlock()
	return nil
}

func runInBackground(r *Relay) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			panic("relay did not stop")
		}
	}
}

func schedulerRelay(store relayStore, w relayWriter, cfg RelayConfig) *Relay {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 20 * time.Millisecond
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 10
	}
	return &Relay{store: store, writer: w, logger: zap.NewNop(), cfg: cfg}
}

// --- R3 ---

// R3: a topic whose Kafka write blocks must not delay the others. The relay used
// to wait for every topic of a poll (wg.Wait) before the next poll, so one write
// stuck for SendTimeout (30 s) held the healthy topics' new rows for as long.
func TestRelay_AHealthyTopicKeepsItsCadenceWhileAnotherTopicsWriteBlocks(t *testing.T) {
	store := &fakeRelayStore{pending: topicRows("stuck.events", 3)}
	w := newGateWriter()
	release := w.block("stuck.events")
	stop := runInBackground(schedulerRelay(store, w, RelayConfig{SendTimeout: time.Minute}))
	defer stop()
	defer close(release) // runs first: the blocked write ends, then the relay stops

	require.Eventually(t, func() bool { return w.enteredCount("stuck.events") == 1 },
		2*time.Second, 5*time.Millisecond, "the stuck topic's write is in flight")

	store.add(topicRows("healthy.events", 4)...)

	// ~2 polls (20 ms each) would do; allow generous slack for a loaded machine.
	require.Eventually(t, func() bool { return w.sentTo("healthy.events") == 4 },
		time.Second, 5*time.Millisecond, "healthy rows created during the block are sent without waiting for it")
	assert.Zero(t, w.sentTo("stuck.events"))
	assert.Equal(t, 1, w.enteredCount("stuck.events"), "the blocked topic is not started a second time")

	store.add(topicRows("healthy.events", 2)...)
	require.Eventually(t, func() bool { return w.sentTo("healthy.events") == 6 }, time.Second, 5*time.Millisecond,
		"and the cadence continues")
}

// R3: more stuck topics than slots must not starve a healthy one. A topic that
// has failed may take all slots but one; once the stuck topics have failed
// (they are then known to be suspect) a healthy topic always has a slot.
func TestRelay_StuckTopicsCannotTakeEverySlot(t *testing.T) {
	store := &fakeRelayStore{}
	for _, topic := range []string{"stuck.a", "stuck.b", "stuck.c"} {
		store.add(topicRows(topic, 2)...)
	}
	w := newGateWriter()
	w.failing["stuck.a"], w.failing["stuck.b"], w.failing["stuck.c"] = true, true, true
	r := schedulerRelay(store, w, RelayConfig{TopicConcurrency: 2, SendTimeout: time.Minute})
	stop := runInBackground(r)
	defer stop()

	// They fail first, so the relay knows them as suspect ...
	require.Eventually(t, func() bool { return r.failingTopics() == 3 }, 2*time.Second, 5*time.Millisecond)
	// ... and then every write to them hangs.
	var releases []chan struct{}
	for _, topic := range []string{"stuck.a", "stuck.b", "stuck.c"} {
		w.mu.Lock()
		w.failing[topic] = false
		w.mu.Unlock()
		releases = append(releases, w.block(topic))
	}
	defer func() {
		for _, ch := range releases {
			close(ch)
		}
	}()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.runningSuspect == 1
	}, 5*time.Second, 5*time.Millisecond, "one suspect topic hangs on the broker")

	store.add(topicRows("healthy.events", 3)...)

	require.Eventually(t, func() bool { return w.sentTo("healthy.events") == 3 },
		time.Second, 5*time.Millisecond, "a healthy topic still has a slot")
	r.mu.Lock()
	assert.LessOrEqual(t, r.runningSuspect, 1, "stuck topics hold at most all slots but one")
	r.mu.Unlock()
}

// R3: a failing topic backs off on its own clock and holds no slot meanwhile.
func TestRelay_AFailingTopicBacksOffExponentiallyAndHoldsNoSlot(t *testing.T) {
	store := &fakeRelayStore{pending: topicRows("broken.events", 2)}
	w := newGateWriter()
	w.failing["broken.events"] = true
	r := schedulerRelay(store, w, RelayConfig{PollInterval: 10 * time.Millisecond, TopicConcurrency: 1})
	stop := runInBackground(r)

	time.Sleep(300 * time.Millisecond)
	stop()

	// Poll 10 ms: attempts at 0, +10, +20, +40, +80, +160 ... — about 6 in 300 ms,
	// not the 30 an every-poll retry would make.
	attempts := w.enteredCount("broken.events")
	assert.GreaterOrEqual(t, attempts, 3)
	assert.LessOrEqual(t, attempts, 9, "a failing topic waits out a growing backoff")

	r.mu.Lock()
	assert.Zero(t, r.running, "nothing holds a slot after the relay stopped")
	r.mu.Unlock()

	assert.Equal(t, 10*time.Millisecond, r.backoff(1))
	assert.Equal(t, 20*time.Millisecond, r.backoff(2))
	assert.Equal(t, 80*time.Millisecond, r.backoff(4))
	assert.Equal(t, topicBackoffCap, r.backoff(40))
}

// Shutdown with forwards in flight stays inside ShutdownFlushTimeout.
func TestRelay_ShutdownWithABlockedTopicStaysInsideTheFlushBudget(t *testing.T) {
	store := &fakeRelayStore{pending: topicRows("stuck.events", 2)}
	w := newGateWriter()
	w.block("stuck.events") // never released
	r := schedulerRelay(store, w, RelayConfig{SendTimeout: time.Minute, ShutdownFlushTimeout: 150 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	require.Eventually(t, func() bool { return w.enteredCount("stuck.events") == 1 }, 2*time.Second, 5*time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
	assert.Less(t, time.Since(start), time.Second, "bounded by the flush timeout, not by SendTimeout")
}

// --- R4 ---

type chunkRecorder struct {
	mu     sync.Mutex
	calls  [][]string // ids per WriteMessages call
	failAt map[int]error
}

func (w *chunkRecorder) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		for _, h := range m.Headers {
			if h.Key == envelope.HeaderEventID {
				ids[i] = string(h.Value)
			}
		}
	}
	w.calls = append(w.calls, ids)
	return w.failAt[len(w.calls)]
}

func withEventIDs(msgs []*Message) []*Message {
	for _, m := range msgs {
		m.Headers = map[string]string{envelope.HeaderEventID: m.ID}
	}
	return msgs
}

func sendOnce(r *Relay, msgs []*Message) ([]string, error) {
	return r.sendFunc()(context.Background(), msgs)
}

// R4: one WriteMessages call never holds more than one kafka-go batch.
func TestRelay_SendsInChunksThatEachFitOneWriterBatch(t *testing.T) {
	msgs := withEventIDs(topicRows("t.events", 5))
	w := &chunkRecorder{}
	r := &Relay{writer: w, logger: zap.NewNop(), writerBatchSize: 2}

	delivered, err := sendOnce(r, msgs)

	require.NoError(t, err)
	require.Len(t, w.calls, 3, "5 rows, 2 per batch: 2 + 2 + 1")
	assert.Len(t, w.calls[0], 2)
	assert.Len(t, w.calls[1], 2)
	assert.Len(t, w.calls[2], 1)
	assert.Equal(t, idsOf(msgs), delivered, "all acknowledged, in order")
}

func TestRelay_ChunksByBytesToo(t *testing.T) {
	msgs := topicRows("t.events", 4)
	for _, m := range msgs {
		m.Payload = make([]byte, 400)
	}
	r := &Relay{writerBatchBytes: 1000} // 90% = 900 by the estimate: two 400-byte rows do not fit with framing

	chunks := r.chunks(msgs)

	for _, c := range chunks {
		var total int64
		for _, m := range c {
			total += estimatedRecordSize(m)
		}
		assert.LessOrEqual(t, total, int64(900), "a chunk stays inside the byte budget")
	}
	var n int
	for _, c := range chunks {
		n += len(c)
	}
	assert.Equal(t, 4, n)
	assert.Greater(t, len(chunks), 1)

	// A row alone over the budget still gets a chunk of its own (kafka-go then
	// sends it alone or refuses it by name).
	big := topicRows("t.events", 1)
	big[0].Payload = make([]byte, 5000)
	assert.Len(t, r.chunks(append(append([]*Message{}, msgs[:1]...), big[0], msgs[1])), 3)
}

// R4: a failed chunk stops the topic. A later chunk must never be sent after an
// earlier one failed: its rows would reach the broker before the failed ones.
func TestRelay_StopsTheTopicAtTheFirstFailedChunk(t *testing.T) {
	msgs := withEventIDs(topicRows("t.events", 5))
	w := &chunkRecorder{failAt: map[int]error{1: errors.New("leader not available")}}
	r := &Relay{writer: w, logger: zap.NewNop(), writerBatchSize: 2}

	delivered, err := sendOnce(r, msgs)

	require.Error(t, err)
	assert.Len(t, w.calls, 1, "nothing of the later chunks is sent")
	assert.Empty(t, delivered)
}

func TestRelay_APartiallyFailedChunkStopsTheTopicAndKeepsItsAcknowledgedPrefix(t *testing.T) {
	msgs := withEventIDs(topicRows("t.events", 5))
	w := &chunkRecorder{failAt: map[int]error{2: kafka.WriteErrors{nil, errors.New("boom")}}}
	r := &Relay{writer: w, logger: zap.NewNop(), writerBatchSize: 2}

	delivered, err := sendOnce(r, msgs)

	require.Error(t, err)
	assert.Len(t, w.calls, 2, "the third chunk is not sent")
	assert.Equal(t, []string{msgs[0].ID, msgs[1].ID, msgs[2].ID}, delivered,
		"chunk 1 whole, row 3 of chunk 2; the failed row 4 and later stay pending")
}

func TestNewRelay_TakesTheBatchLimitsFromTheWriter(t *testing.T) {
	w := &kafka.Writer{Addr: kafka.TCP("x:9092"), RequiredAcks: kafka.RequireAll, Balancer: &kafka.Hash{},
		BatchSize: 7, BatchBytes: 12345}
	r := NewRelay(nil, w, zap.NewNop(), RelayConfig{})
	assert.Equal(t, 7, r.batchLimit())
	assert.EqualValues(t, 12345, r.batchBytesLimit())

	d := NewRelay(nil, &kafka.Writer{Addr: kafka.TCP("x:9092"), RequiredAcks: kafka.RequireAll, Balancer: &kafka.Hash{}},
		zap.NewNop(), RelayConfig{})
	assert.Equal(t, 100, d.batchLimit(), "kafka-go's default")
	assert.EqualValues(t, 1<<20, d.batchBytesLimit())
}

// --- R5 ---

func lazyPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db")
	require.NoError(t, err)
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg) // does not dial
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestNewRelay_CapsTopicConcurrencyToAQuarterOfThePoolAndSaysSo(t *testing.T) {
	w := &kafka.Writer{Addr: kafka.TCP("x:9092"), RequiredAcks: kafka.RequireAll, Balancer: &kafka.Hash{}}
	for _, tc := range []struct {
		maxConns   int32
		configured int
		want       int
		capped     bool
	}{
		{maxConns: 40, configured: 4, want: 4},
		{maxConns: 10, configured: 4, want: 2, capped: true},
		{maxConns: 4, configured: 4, want: 1, capped: true},
		{maxConns: 2, configured: 4, want: 1, capped: true},
		{maxConns: 8, configured: 0, want: 2, capped: true}, // the default 4
		{maxConns: 8, configured: 2, want: 2},
	} {
		core, logs := observer.New(zapcore.WarnLevel)
		r := NewRelay(NewStore(lazyPool(t, tc.maxConns)), w, zap.New(core), RelayConfig{TopicConcurrency: tc.configured})
		assert.Equal(t, tc.want, r.topicConcurrency(), "pool %d configured %d", tc.maxConns, tc.configured)
		if tc.capped {
			require.Equal(t, 1, logs.Len(), "the cap is logged once")
			assert.Contains(t, logs.All()[0].Message, "capped")
		} else {
			assert.Zero(t, logs.Len())
		}
	}
}

// --- Busy and too-large ---

type busyStore struct{ calls atomic.Int32 }

func (s *busyStore) PendingTopics(context.Context) ([]string, error) {
	return []string{"held.events"}, nil
}
func (s *busyStore) ForwardTopic(context.Context, string, int, SendFunc) (ForwardResult, error) {
	s.calls.Add(1)
	return ForwardResult{Busy: true}, nil
}

// R2: a topic held by another relay is counted, so one held for ever shows.
func TestRelay_CountsAndEventuallyLogsATopicThatStaysBusy(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	store := &busyStore{}
	r := &Relay{store: store, writer: noopRelayWriter{}, logger: zap.New(core),
		cfg: RelayConfig{PollInterval: 5 * time.Millisecond, SendTimeout: 10 * time.Millisecond}}
	before := topicCounterOf(t, "outbox_topic_busy_total", "held.events")

	stop := runInBackground(r)
	require.Eventually(t, func() bool {
		return logs.FilterLevelExact(zapcore.WarnLevel).Len() > 0
	}, 2*time.Second, 5*time.Millisecond, "a topic busy for longer than any forward can take is logged at Warn")
	stop()

	assert.Greater(t, topicCounterOf(t, "outbox_topic_busy_total", "held.events"), before+2)
	warn := logs.FilterLevelExact(zapcore.WarnLevel).All()[0]
	assert.Equal(t, "held.events", warn.ContextMap()["topic"])
	assert.Zero(t, r.failingTopics(), "busy is not a failure")
}

// R10: kafka-go refuses a row for its size: counted every time, named in the log.
func TestRelay_NamesTheRowKafkaRefusedForItsSize(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	rows := withEventIDs(topicRows("big.events", 2))
	tooLarge := kafka.MessageTooLargeError{Message: kafka.Message{
		Topic: "big.events", Key: []byte("k"), Value: make([]byte, 2_000_000),
		Headers: []kafka.Header{{Key: envelope.HeaderEventID, Value: []byte("evt-123")}},
	}}
	r := &Relay{writer: &chunkRecorder{failAt: map[int]error{1: tooLarge, 2: tooLarge}}, logger: zap.New(core)}
	before := topicCounterOf(t, "outbox_topic_message_too_large_total", "big.events")

	_, err := sendOnce(r, rows)
	require.Error(t, err)
	_, err = sendOnce(r, rows) // the next attempt: kafka-go fails the same way
	require.Error(t, err)

	assert.Equal(t, before+2, topicCounterOf(t, "outbox_topic_message_too_large_total", "big.events"), "counted every time")
	require.Equal(t, 1, logs.Len(), "logged once per rate-limit window")
	e := logs.All()[0]
	assert.Equal(t, "evt-123", e.ContextMap()["event_id"], "the log names the stuck row")
	assert.Equal(t, "big.events", e.ContextMap()["topic"])
}
