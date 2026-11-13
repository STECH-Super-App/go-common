package outbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// SL-15: ShutdownFlushTimeout and MetricsInterval were defended against a zero
// value; BatchSize, PollInterval and the reaper Interval were not. With
// BatchSize == 0 the relay fetched LIMIT 0, saw processed(0) >= BatchSize(0)
// and `continue`d without sleeping — a hot loop against Postgres that forwards
// nothing.

type countingRelayStore struct {
	mu        sync.Mutex
	fetches   int
	lastBatch int
}

func (s *countingRelayStore) FetchPending(_ context.Context, batchSize int) ([]*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetches++
	s.lastBatch = batchSize
	return nil, nil
}

func (s *countingRelayStore) MarkSentBatch(context.Context, []string) error { return nil }

func (s *countingRelayStore) snapshot() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches, s.lastBatch
}

type noopRelayWriter struct{}

func (noopRelayWriter) WriteMessages(context.Context, ...kafka.Message) error { return nil }

func TestRelayRun_ZeroConfigDoesNotSpin(t *testing.T) {
	store := &countingRelayStore{}
	r := &Relay{store: store, writer: noopRelayWriter{}, logger: zap.NewNop(), cfg: RelayConfig{}}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	fetches, batch := store.snapshot()
	assert.LessOrEqual(t, fetches, 2, "a zero RelayConfig must poll at the default interval, not in a hot loop")
	assert.Equal(t, DefaultBatchSize, batch, "a zero BatchSize must become the default, not LIMIT 0")
}

func TestRelayRun_NegativeConfigDoesNotSpin(t *testing.T) {
	store := &countingRelayStore{}
	r := &Relay{store: store, writer: noopRelayWriter{}, logger: zap.NewNop(),
		cfg: RelayConfig{PollInterval: -time.Second, BatchSize: -5}}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	fetches, batch := store.snapshot()
	assert.LessOrEqual(t, fetches, 2)
	assert.Equal(t, DefaultBatchSize, batch)
}

// fakeReaperStore records what the reaper asked for.
type fakeReaperStore struct {
	mu            sync.Mutex
	sentCalls     int
	processedArgs []processedCall
	// processedPlan is the row count each DeleteProcessed call returns, in
	// order; once exhausted it returns 0.
	processedPlan []int64
	processedErr  error
}

type processedCall struct {
	retention time.Duration
	limit     int
}

func (s *fakeReaperStore) DeleteSent(context.Context, time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentCalls++
	return 0, nil
}

func (s *fakeReaperStore) DeleteProcessed(_ context.Context, retention time.Duration, limit int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processedArgs = append(s.processedArgs, processedCall{retention, limit})
	if s.processedErr != nil {
		return 0, s.processedErr
	}
	if len(s.processedPlan) == 0 {
		return 0, nil
	}
	n := s.processedPlan[0]
	s.processedPlan = s.processedPlan[1:]
	return n, nil
}

func (s *fakeReaperStore) calls() (sent int, processed []processedCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sentCalls, append([]processedCall(nil), s.processedArgs...)
}

func TestReaperRun_ZeroIntervalDoesNotSpin(t *testing.T) {
	store := &fakeReaperStore{}
	r := &Reaper{store: store, logger: zap.NewNop(), cfg: ReaperConfig{}}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	sent, _ := store.calls()
	assert.LessOrEqual(t, sent, 2, "a zero Interval must wait the default 5m between cycles")
}

func TestReaper_ZeroRetentionDoesNotDeleteEverything(t *testing.T) {
	// Retention 0 would make the cutoff "now": every sent row — and every
	// processed claim — would be deleted on the first cycle.
	store := &fakeReaperStore{}
	r := &Reaper{store: store, logger: zap.NewNop(), cfg: ReaperConfig{}}

	r.cleanup(context.Background())

	_, processed := store.calls()
	require.NotEmpty(t, processed)
	assert.Equal(t, DefaultProcessedRetention, processed[0].retention)
	assert.Equal(t, DefaultProcessedBatchSize, processed[0].limit)
}

// SL-09: CleanupProcessed was documented as "called automatically by the
// Reaper"; the Reaper never called it and neither did any service, so
// processed_outbox_messages grew by a row per consumed event forever.
func TestReaper_DeletesProcessedClaimsInBoundedBatchesUntilDrained(t *testing.T) {
	store := &fakeReaperStore{processedPlan: []int64{3, 3, 1}}
	r := &Reaper{store: store, logger: zap.NewNop(), cfg: ReaperConfig{
		Retention:          time.Hour,
		ProcessedRetention: 48 * time.Hour,
		ProcessedBatchSize: 3,
	}}

	r.cleanup(context.Background())

	_, processed := store.calls()
	require.Len(t, processed, 3, "a full batch means more may remain; the first short batch ends the cycle")
	for _, c := range processed {
		assert.Equal(t, 48*time.Hour, c.retention)
		assert.Equal(t, 3, c.limit)
	}
}

func TestReaper_ProcessedCleanupIsCappedPerCycle(t *testing.T) {
	plan := make([]int64, maxProcessedBatchesPerCycle*2)
	for i := range plan {
		plan[i] = 10
	}
	store := &fakeReaperStore{processedPlan: plan}
	r := &Reaper{store: store, logger: zap.NewNop(), cfg: ReaperConfig{ProcessedBatchSize: 10}}

	r.cleanup(context.Background())

	_, processed := store.calls()
	assert.Len(t, processed, maxProcessedBatchesPerCycle,
		"a huge backlog must be drained over several cycles, not in one unbounded loop")
}

func TestReaper_MissingProcessedTableIsNotAnErrorEveryCycle(t *testing.T) {
	// A service whose database has no processed_outbox_messages (it never
	// consumed, or keeps its own outbox tables) must not log an error each
	// cycle; the reaper stops asking.
	store := &fakeReaperStore{processedErr: ErrProcessedTableMissing}
	r := &Reaper{store: store, logger: zap.NewNop(), cfg: ReaperConfig{}}

	r.cleanup(context.Background())
	r.cleanup(context.Background())

	_, processed := store.calls()
	assert.Len(t, processed, 1)
}
