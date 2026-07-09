package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeReaperStore records DeleteSent / ReleaseStuck calls so tests can assert
// a cleanup cycle without Postgres.
type fakeReaperStore struct {
	deleteSentCalls []time.Duration
	deleteSentErr   error

	releaseStuckCalls []time.Duration
	releaseStuckErr   error
}

func (s *fakeReaperStore) DeleteSent(_ context.Context, retention time.Duration) (int64, error) {
	s.deleteSentCalls = append(s.deleteSentCalls, retention)
	return 1, s.deleteSentErr
}

func (s *fakeReaperStore) ReleaseStuck(_ context.Context, olderThan time.Duration) (int64, error) {
	s.releaseStuckCalls = append(s.releaseStuckCalls, olderThan)
	return 1, s.releaseStuckErr
}

// fakeCleaner records CleanupProcessed calls (stands in for *Deduplicator).
type fakeCleaner struct {
	calls []time.Duration
	err   error
}

func (c *fakeCleaner) CleanupProcessed(_ context.Context, retention time.Duration) (int64, error) {
	c.calls = append(c.calls, retention)
	return 1, c.err
}

func TestReaper_cleanup_runsAllSteps(t *testing.T) {
	store := &fakeReaperStore{}
	cleaner := &fakeCleaner{}
	r := &Reaper{
		store:  store,
		dedup:  cleaner,
		logger: zap.NewNop(),
		cfg:    ReaperConfig{Retention: 72 * time.Hour, ClaimTimeout: 5 * time.Minute},
	}

	r.cleanup(context.Background())

	if len(store.deleteSentCalls) != 1 || store.deleteSentCalls[0] != 72*time.Hour {
		t.Errorf("DeleteSent calls = %v, want one call with 72h", store.deleteSentCalls)
	}
	if len(store.releaseStuckCalls) != 1 || store.releaseStuckCalls[0] != 5*time.Minute {
		t.Errorf("ReleaseStuck calls = %v, want one call with 5m", store.releaseStuckCalls)
	}
	if len(cleaner.calls) != 1 || cleaner.calls[0] != 72*time.Hour {
		t.Errorf("CleanupProcessed calls = %v, want one call with 72h", cleaner.calls)
	}
}

func TestReaper_cleanup_withoutDeduplicator(t *testing.T) {
	store := &fakeReaperStore{}
	r := &Reaper{
		store:  store,
		logger: zap.NewNop(),
		cfg:    ReaperConfig{Retention: time.Hour, ClaimTimeout: time.Minute},
	}

	// Must not panic and must still run the outbox-table steps.
	r.cleanup(context.Background())

	if len(store.deleteSentCalls) != 1 {
		t.Errorf("DeleteSent calls = %v, want 1", store.deleteSentCalls)
	}
	if len(store.releaseStuckCalls) != 1 {
		t.Errorf("ReleaseStuck calls = %v, want 1", store.releaseStuckCalls)
	}
}

// TestReaper_cleanup_stepsAreIndependent: a failure in one step must not stop
// the following steps — DeleteSent, ReleaseStuck and dedup cleanup guard
// against different failure modes.
func TestReaper_cleanup_stepsAreIndependent(t *testing.T) {
	store := &fakeReaperStore{
		deleteSentErr:   errors.New("delete failed"),
		releaseStuckErr: errors.New("release failed"),
	}
	cleaner := &fakeCleaner{}
	r := &Reaper{
		store:  store,
		dedup:  cleaner,
		logger: zap.NewNop(),
		cfg:    ReaperConfig{Retention: time.Hour, ClaimTimeout: time.Minute},
	}

	r.cleanup(context.Background())

	if len(store.releaseStuckCalls) != 1 {
		t.Errorf("ReleaseStuck must run despite DeleteSent failing")
	}
	if len(cleaner.calls) != 1 {
		t.Errorf("CleanupProcessed must run despite earlier steps failing")
	}
}

// TestNewReaper_claimTimeoutZeroValueGuard: services that build ReaperConfig
// literally (e.g. order-service main.go) leave ClaimTimeout at its zero value;
// NewReaper must default it so the crash backstop is never silently disabled.
func TestNewReaper_claimTimeoutZeroValueGuard(t *testing.T) {
	r := NewReaper(nil, zap.NewNop(), ReaperConfig{Interval: time.Minute, Retention: time.Hour})
	if r.cfg.ClaimTimeout != DefaultClaimTimeout {
		t.Errorf("ClaimTimeout = %v, want default %v", r.cfg.ClaimTimeout, DefaultClaimTimeout)
	}

	r = NewReaper(nil, zap.NewNop(), ReaperConfig{ClaimTimeout: -1})
	if r.cfg.ClaimTimeout != DefaultClaimTimeout {
		t.Errorf("negative ClaimTimeout = %v, want default %v", r.cfg.ClaimTimeout, DefaultClaimTimeout)
	}

	r = NewReaper(nil, zap.NewNop(), ReaperConfig{ClaimTimeout: 90 * time.Second})
	if r.cfg.ClaimTimeout != 90*time.Second {
		t.Errorf("explicit ClaimTimeout = %v, want 90s", r.cfg.ClaimTimeout)
	}
}

func TestWithDeduplicator_nilIsIgnored(t *testing.T) {
	r := NewReaper(nil, zap.NewNop(), ReaperConfig{}, WithDeduplicator(nil))
	if r.dedup != nil {
		t.Error("WithDeduplicator(nil) must leave the cleaner unset")
	}

	d := &Deduplicator{}
	r = NewReaper(nil, zap.NewNop(), ReaperConfig{}, WithDeduplicator(d))
	if r.dedup == nil {
		t.Error("WithDeduplicator must attach the cleaner")
	}
}
