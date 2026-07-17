package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessage_StatusConstants(t *testing.T) {
	assert.Equal(t, Status("pending"), StatusPending)
	assert.Equal(t, Status("processing"), StatusProcessing)
	assert.Equal(t, Status("sent"), StatusSent)
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg)

	// Default relay config
	assert.Equal(t, 100, cfg.Relay.BatchSize)
	assert.True(t, cfg.Relay.PollInterval > 0, "PollInterval should be positive")

	// Default reaper config
	assert.True(t, cfg.Reaper.Interval > 0, "Reaper Interval should be positive")
	assert.True(t, cfg.Reaper.Retention > 0, "Reaper Retention should be positive")
	assert.Equal(t, DefaultClaimTimeout, cfg.Reaper.ClaimTimeout)
}

// TestSortMessagesByCreatedAt covers the Go-side re-sort after ClaimPending's
// RETURNING clause, whose row order Postgres leaves unspecified.
func TestSortMessagesByCreatedAt(t *testing.T) {
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	msgs := []*Message{
		{ID: "third", CreatedAt: base.Add(2 * time.Second)},
		{ID: "first", CreatedAt: base},
		{ID: "second", CreatedAt: base.Add(time.Second)},
	}

	sortMessagesByCreatedAt(msgs)

	got := []string{msgs[0].ID, msgs[1].ID, msgs[2].ID}
	assert.Equal(t, []string{"first", "second", "third"}, got)
}

func TestRunInTx_NilPool(t *testing.T) {
	// RunInTx with a nil pool bypasses the transaction and calls fn(nil).
	// This supports test/dev scenarios where no database is available.
	called := false
	err := RunInTx(context.Background(), nil, func(tx Tx) error {
		called = true
		assert.Nil(t, tx, "tx should be nil when pool is nil")
		return nil
	})

	require.NoError(t, err)
	assert.True(t, called, "fn should have been called")
}
