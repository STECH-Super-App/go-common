package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

// SL-26: the outbox returned raw fmt.Errorf errors, against Critical Rule 7
// (typed errors only: "every error from a service or shared library"). A caller
// that forwarded one to response.JSONError got a Reason-less 500.

// deadPool is a pool whose every statement fails fast: nothing listens on
// port 1 and pgxpool connects lazily, so construction succeeds.
func deadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func requireTyped(t *testing.T, err error, wantReason string) {
	t.Helper()
	require.Error(t, err)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "want an *errors.AppError, got %T: %v", err, err)
	assert.Equal(t, wantReason, ae.Reason)
	assert.Equal(t, 500, ae.Code)
	assert.NotNil(t, ae.Unwrap(), "the driver error stays reachable as the cause")
}

func TestStore_FailuresAreTypedWithTheCause(t *testing.T) {
	store := outbox.NewStore(deadPool(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := store.FetchPending(ctx, 10)
	requireTyped(t, err, outbox.ReasonFetchFailed)

	_, _, err = store.PendingStats(ctx)
	requireTyped(t, err, outbox.ReasonStatsFailed)

	err = store.MarkSent(ctx, "3b4f9c6e-0000-4000-8000-000000000001")
	requireTyped(t, err, outbox.ReasonMarkFailed)

	err = store.MarkSentBatch(ctx, []string{"3b4f9c6e-0000-4000-8000-000000000001"})
	requireTyped(t, err, outbox.ReasonMarkFailed)

	_, err = store.DeleteSent(ctx, time.Hour)
	requireTyped(t, err, outbox.ReasonCleanupFailed)

	_, err = store.DeleteProcessed(ctx, time.Hour, 10)
	requireTyped(t, err, outbox.ReasonCleanupFailed)
}

func TestRunInTx_BeginFailureIsTyped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := outbox.RunInTx(ctx, deadPool(t), func(outbox.Tx) error { return nil })

	requireTyped(t, err, outbox.ReasonTxFailed)
}

func TestRunInTx_HandlerErrorPassesThroughUntouched(t *testing.T) {
	// The handler's own error must reach the caller as it is (ErrPoisonPill and
	// friends are matched with errors.Is after the transaction wrapper).
	sentinel := errors.New("handler failed")

	err := outbox.RunInTx(context.Background(), nil, func(outbox.Tx) error { return sentinel })

	assert.Same(t, sentinel, err)
}

func TestInsertTx_WrongTxTypeIsTyped(t *testing.T) {
	err := outbox.NewStore(nil).InsertTx(context.Background(), "not a pgx.Tx", &outbox.Message{})

	require.Error(t, err)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, outbox.ReasonTxInvalid, ae.Reason)
}

func TestPublishProto_FailuresAreTyped(t *testing.T) {
	pub := outbox.NewPublisher(outbox.NewTestStore(), "t")

	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{})

	require.Error(t, err)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, outbox.ReasonPublishInvalid, ae.Reason)
}

func TestDeduplicator_ClaimFailureIsTypedAndHandlerErrorsAreNot(t *testing.T) {
	d := outbox.NewDeduplicator(deadPool(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := d.Process(ctx, "3b4f9c6e-0000-4000-8000-000000000002", func() error { return nil })
	require.Error(t, err)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "begin tx failure must be typed, got %T", err)

	_, err = d.CleanupProcessed(ctx, time.Hour)
	requireTyped(t, err, outbox.ReasonCleanupFailed)
}
