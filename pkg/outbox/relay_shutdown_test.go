package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ctxStore is a relayStore that, like Postgres, refuses work on a done
// context. It hands out its batch once, then nothing.
type ctxStore struct {
	mu         sync.Mutex
	pending    []*Message
	fetchCalls int
	markedIDs  []string
	onFetch    func()
}

func (s *ctxStore) FetchPending(ctx context.Context, _ int) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.fetchCalls++
	batch := s.pending
	s.pending = nil
	onFetch := s.onFetch
	s.mu.Unlock()
	if onFetch != nil {
		onFetch()
	}
	return batch, nil
}

func (s *ctxStore) MarkSentBatch(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markedIDs = append(s.markedIDs, ids...)
	return nil
}

func (s *ctxStore) snapshot() (fetches int, marked []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchCalls, append([]string(nil), s.markedIDs...)
}

// ctxWriter is a relayWriter that, like kafka-go, fails on a done context.
// It waits `delay` (ctx-aware) before answering, then returns result.
type ctxWriter struct {
	delay  time.Duration
	result error
}

func (w *ctxWriter) WriteMessages(ctx context.Context, _ ...kafka.Message) error {
	if w.delay > 0 {
		select {
		case <-time.After(w.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.result
}

func shutdownTestRelay(store relayStore, writer relayWriter, flush time.Duration) *Relay {
	return &Relay{
		store:  store,
		writer: writer,
		logger: zap.NewNop(),
		cfg:    RelayConfig{PollInterval: time.Hour, BatchSize: 10, ShutdownFlushTimeout: flush},
	}
}

func idsOf(msgs []*Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

// The shutdown arrives right after a batch was fetched — while Kafka is
// receiving it. Before the fix the write and the mark-sent both ran on the
// cancelled context: the rows Kafka already held stayed pending and were
// re-published on the next start. Now the batch is written and marked, and no
// further batch is fetched.
func TestRelayRun_FinishesFetchedBatchAfterCancel(t *testing.T) {
	batch := pendingMessagesFixture(3)
	ctx, cancel := context.WithCancel(context.Background())
	store := &ctxStore{pending: batch, onFetch: cancel}
	writer := &ctxWriter{delay: 50 * time.Millisecond}

	err := shutdownTestRelay(store, writer, time.Second).Run(ctx)

	require.ErrorIs(t, err, context.Canceled)
	fetches, marked := store.snapshot()
	assert.Equal(t, idsOf(batch), marked, "an already-fetched batch must be marked sent despite the cancel")
	assert.Equal(t, 1, fetches, "no new batch may be fetched after the cancel")
}

// The partial-failure arm (kafka.WriteErrors) marks its delivered half on the
// same detached context.
func TestRelayRun_PartialBatchMarkedAfterCancel(t *testing.T) {
	batch := pendingMessagesFixture(2)
	ctx, cancel := context.WithCancel(context.Background())
	store := &ctxStore{pending: batch, onFetch: cancel}
	writer := &ctxWriter{
		delay:  20 * time.Millisecond,
		result: kafka.WriteErrors{nil, errors.New("unknown topic or partition")},
	}

	err := shutdownTestRelay(store, writer, time.Second).Run(ctx)

	require.ErrorIs(t, err, context.Canceled)
	_, marked := store.snapshot()
	assert.Equal(t, []string{batch[0].ID}, marked, "the delivered half must be marked sent despite the cancel")
}

// The flush is bounded: a write that never finishes is abandoned
// ShutdownFlushTimeout after the cancel, so a dead broker cannot hold the
// process past its shutdown budget. Nothing is marked — the rows stay pending.
func TestRelayRun_FlushBoundedByShutdownFlushTimeout(t *testing.T) {
	batch := pendingMessagesFixture(2)
	ctx, cancel := context.WithCancel(context.Background())
	store := &ctxStore{pending: batch, onFetch: cancel}
	writer := &ctxWriter{delay: time.Hour}

	start := time.Now()
	err := shutdownTestRelay(store, writer, 100*time.Millisecond).Run(ctx)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, elapsed, 90*time.Millisecond, "the flush must get its grace window")
	assert.Less(t, elapsed, 2*time.Second, "the flush must not outlive ShutdownFlushTimeout")
	_, marked := store.snapshot()
	assert.Empty(t, marked)
}

// The bound starts at cancellation, not at fetch: while the relay runs
// normally a write slower than ShutdownFlushTimeout still completes and is
// marked, so a slow broker cannot make every batch time out and re-publish.
func TestPollAndForward_NoFlushBoundWithoutCancel(t *testing.T) {
	batch := pendingMessagesFixture(2)
	store := &ctxStore{pending: batch}
	writer := &ctxWriter{delay: 150 * time.Millisecond}

	processed, err := shutdownTestRelay(store, writer, 20*time.Millisecond).
		pollAndForward(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	_, marked := store.snapshot()
	assert.Equal(t, idsOf(batch), marked)
}

// The flush context keeps the caller's values (trace/logging state) while
// dropping its cancellation.
func TestFlushContext_DetachedButKeepsValues(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	r := shutdownTestRelay(nil, nil, time.Hour)

	flushCtx, release := r.flushContext(parent)
	defer release()
	cancel()

	assert.NoError(t, flushCtx.Err(), "the flush context must outlive the parent's cancel")
	assert.Equal(t, "v", flushCtx.Value(key{}))
}

func TestRelayConfig_ShutdownFlushTimeoutDefault(t *testing.T) {
	assert.Equal(t, DefaultShutdownFlushTimeout, shutdownTestRelay(nil, nil, 0).shutdownFlushTimeout(),
		"a hand-built zero RelayConfig must fall back to the default, not abandon every batch")
	assert.Equal(t, DefaultShutdownFlushTimeout, shutdownTestRelay(nil, nil, -time.Second).shutdownFlushTimeout())
	assert.Equal(t, 3*time.Second, shutdownTestRelay(nil, nil, 3*time.Second).shutdownFlushTimeout())
	assert.Equal(t, 5*time.Second, DefaultConfig().Relay.ShutdownFlushTimeout)
}

type nopReaperStore struct{}

func (nopReaperStore) DeleteSent(context.Context, time.Duration) (int64, error) { return 0, nil }

// Outbox.Run is a blocking Start: it returns ctx.Err() on cancel, and only
// after the relay has finished the batch it was writing.
func TestOutboxRun_BlocksUntilCancelAndFlushes(t *testing.T) {
	batch := pendingMessagesFixture(2)
	fetched := make(chan struct{})
	var once sync.Once
	store := &ctxStore{pending: batch, onFetch: func() { once.Do(func() { close(fetched) }) }}
	o := &Outbox{
		relay:   shutdownTestRelay(store, &ctxWriter{delay: 100 * time.Millisecond}, time.Second),
		reaper:  &Reaper{store: nopReaperStore{}, logger: zap.NewNop(), cfg: ReaperConfig{Interval: time.Hour}},
		sampler: newSampler(func(context.Context) (int64, float64, error) { return 0, 0, nil }, time.Hour, zap.NewNop()),
		logger:  zap.NewNop(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	select {
	case <-fetched:
	case <-time.After(2 * time.Second):
		t.Fatal("relay never fetched")
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned before cancel: %v", err)
	default:
	}
	cancel() // mid-write: the writer is still inside its 100 ms delay

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	_, marked := store.snapshot()
	assert.Equal(t, idsOf(batch), marked, "Run must not return before the in-flight batch is marked sent")
}
