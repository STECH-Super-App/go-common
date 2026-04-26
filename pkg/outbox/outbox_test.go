package outbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessage_StatusConstants(t *testing.T) {
	assert.Equal(t, Status("pending"), StatusPending)
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
