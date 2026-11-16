package events_test

// HandlePrepared against a REAL Postgres: the slow phase holds no transaction
// and no connection, and the two phases keep every guarantee of HandleTx.
// Kafka is a fake reader; the properties under test are the database's.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

type prepared = struct{ path string }

func effectsTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `CREATE TABLE effects (n int)`)
	require.NoError(t, err)
}

func idleInTransaction(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM pg_stat_activity
		  WHERE datname = current_database() AND state LIKE 'idle in transaction%'`).Scan(&n))
	return n
}

// runBackground runs disp and returns its result channel and a cancel.
func runBackground(disp *events.Dispatcher) (<-chan error, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	return done, cancel
}

func waitCommits(t *testing.T, reader *greedyReader, n int) {
	t.Helper()
	require.Eventually(t, func() bool { _, c := reader.snapshot(); return len(c) >= n }, 5*time.Second, 5*time.Millisecond)
}

// The point of the form: while prepare runs, the library holds no connection and
// no transaction. The control is HandleTx doing the same wait, which leaves one
// session idle in transaction — so the probe can tell the two apart.
func TestPGPrepared_NoConnectionOrTransactionIsHeldDuringPrepare(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-slow-prepare")}}
	entered, release := make(chan struct{}), make(chan struct{})
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (prepared, error) {
			close(entered)
			<-release
			return prepared{path: "/tmp/out.mp4"}, nil
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, p prepared) error {
			assert.Equal(t, "/tmp/out.mp4", p.path, "prepare's result reaches apply")
			_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
			return err
		})

	done, cancel := runBackground(disp)
	defer cancel()
	<-entered
	// Prepare is blocked right now.
	assert.Zero(t, pool.Stat().AcquiredConns(), "no pooled connection is held")
	assert.Zero(t, idleInTransaction(t, pool), "no transaction is open")
	assert.Zero(t, count(t, pool, "processed_events"), "nothing is claimed yet")
	close(release)
	waitCommits(t, reader, 1)
	cancel()
	require.ErrorIs(t, awaitRun(t, done), context.Canceled)

	assert.Equal(t, 1, count(t, pool, "effects"))
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

func TestPGPrepared_ControlHandleTxHoldsAnIdleTransactionWhileItWaits(t *testing.T) {
	pool := migratedPool(t)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-control")}}
	entered, release := make(chan struct{}), make(chan struct{})
	disp := pgDisp(pool, reader)
	events.HandleTx(disp, func(context.Context, events.Tx, *usersv1.UserRegistered) error {
		close(entered)
		<-release
		return nil
	})
	done, cancel := runBackground(disp)
	defer cancel()
	<-entered

	assert.Equal(t, 1, idleInTransaction(t, pool), "HandleTx keeps the transaction open through its handler")
	assert.EqualValues(t, 1, pool.Stat().AcquiredConns())

	close(release)
	waitCommits(t, reader, 1)
	cancel()
	require.ErrorIs(t, awaitRun(t, done), context.Canceled)
}

// The effect and the processed mark are one unit: an apply that fails after
// writing leaves neither, and the retry — which runs prepare AGAIN — writes once.
func TestPGPrepared_ApplyFailureRollsBackEffectAndMarkAndRerunsPrepare(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-apply-fails")}}
	var prepares, applies atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			prepares.Add(1)
			return struct{}{}, nil
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			if _, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`); err != nil {
				return err
			}
			if applies.Add(1) == 1 {
				assert.Zero(t, count(t, pool, "processed_events"), "the claim is visible inside the open transaction only")
				return errors.New("write failed after the insert")
			}
			return nil
		})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, applies.Load())
	assert.EqualValues(t, 2, prepares.Load(), "a retry runs prepare again: nothing is cached across attempts")
	assert.Equal(t, 1, count(t, pool, "effects"), "the failed attempt's insert was rolled back")
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// A commit the database refuses: both phases run again, the effect lands once.
func TestPGPrepared_FailedCommitRunsPrepareAndApplyAgainEffectOnce(t *testing.T) {
	pool := migratedPool(t)
	refuseCommitsWhileFlagged(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-commit-refused")}}
	var prepares, applies atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			if prepares.Add(1) == 2 {
				assert.Zero(t, count(t, pool, "effects"), "the refused commit left no effect")
				assert.Zero(t, count(t, pool, "processed_events"), "and no mark")
				clearFlag(t, pool)
			}
			return struct{}{}, nil
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			applies.Add(1)
			_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
			return err
		})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, prepares.Load())
	assert.EqualValues(t, 2, applies.Load())
	assert.Equal(t, 1, count(t, pool, "effects"))
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// A redelivery of a processed event stops at the read-only check: prepare — the
// slow part — does not run.
func TestPGPrepared_RedeliveryAfterCommitSkipsPrepare(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-twice"), userRegisteredMsg(t, "e-twice")}}
	var prepares, applies atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			prepares.Add(1)
			return struct{}{}, nil
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			applies.Add(1)
			_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
			return err
		})

	done, cancel := runBackground(disp)
	defer cancel()
	waitCommits(t, reader, 2)
	cancel()
	<-done

	assert.EqualValues(t, 1, prepares.Load(), "the second delivery never reached prepare")
	assert.EqualValues(t, 1, applies.Load())
	assert.Equal(t, 1, count(t, pool, "effects"))
}

// An event the previous deduplicator recorded skips prepare too (the legacy-table
// bridge).
func TestPGPrepared_LegacyTableHitSkipsPrepare(t *testing.T) {
	pool := migratedPool(t)
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(), `INSERT INTO processed_outbox_messages (event_id) VALUES ($1)`, id)
	require.NoError(t, err)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, id)}}
	var calls atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) { calls.Add(1); return struct{}{}, nil },
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { calls.Add(1); return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Zero(t, calls.Load(), "processed before the upgrade: neither phase runs")
}

// Several pods get the same event at once. Prepare may run in each (it is
// idempotent by contract); the claim lets exactly one apply.
func TestPGPrepared_ConcurrentDuplicateDeliveriesApplyOnce(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	var prepares, applies atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-race")}}
		disp := pgDisp(pool, reader)
		events.HandlePrepared(disp,
			func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
				prepares.Add(1)
				time.Sleep(100 * time.Millisecond) // let every delivery reach the claim together
				return struct{}{}, nil
			},
			func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
				applies.Add(1)
				time.Sleep(50 * time.Millisecond)
				_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
				return err
			})
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = runOne(t, disp, reader)
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, applies.Load(), "the claim found the event processed for every loser: its apply was skipped")
	assert.GreaterOrEqual(t, prepares.Load(), int32(2), "the slow phase ran concurrently: prepare must be safe to repeat")
	assert.Equal(t, 1, count(t, pool, "effects"))
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// A poison pill from prepare is dead-lettered at once: one attempt, no apply, no
// claim.
func TestPGPrepared_PoisonFromPrepareIsDeadLetteredAtOnce(t *testing.T) {
	pool := migratedPool(t)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-poison")}}
	dlq := &fakeWriter{}
	var prepares, applies atomic.Int32
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq,
		events.WithRetryWindow(5*time.Second), events.WithRetryBackoff(time.Second, time.Second))
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			prepares.Add(1)
			return struct{}{}, fmt.Errorf("source file is corrupt: %w", events.ErrPoisonPill)
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { applies.Add(1); return nil })

	started := time.Now()
	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Less(t, time.Since(started), time.Second, "no in-place retry wait")
	assert.EqualValues(t, 1, prepares.Load())
	assert.Zero(t, applies.Load())
	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, "poison_pill", string(findTestHeader(dlq.captured()[0], "x-dlq-reason")))
	assert.Zero(t, count(t, pool, "processed_events"), "a dead-lettered event is not marked processed")
}

func TestPGPrepared_UndecodablePayloadIsDeadLetteredWithoutPrepare(t *testing.T) {
	pool := migratedPool(t)
	msg := userRegisteredMsg(t, "e-garbage")
	msg.Value = []byte(`{not json`)
	reader := &greedyReader{msgs: []kafka.Message{msg}}
	dlq := &fakeWriter{}
	var calls atomic.Int32
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) { calls.Add(1); return struct{}{}, nil },
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { calls.Add(1); return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Zero(t, calls.Load())
	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, "unmarshal_error", string(findTestHeader(dlq.captured()[0], "x-dlq-reason")))
}

// A prepare that gives up at shutdown (ErrLeaveUncommitted, from its own signal —
// the handler context is detached from Run's) leaves the message untouched: no
// commit, no dead letter, no claim, and the next message is not fetched.
func TestPGPrepared_ShutdownDuringPrepareLeavesTheMessageUncommitted(t *testing.T) {
	pool := migratedPool(t)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-leave"), userRegisteredMsg(t, "e-next")}}
	dlq := &fakeWriter{}
	stopping := make(chan struct{})
	entered := make(chan struct{})
	var applies atomic.Int32
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq, events.WithGroup("g"))
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			close(entered)
			<-stopping
			return struct{}{}, leaveAtShutdown()
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { applies.Add(1); return nil })

	done, cancel := runBackground(disp)
	defer cancel()
	<-entered
	close(stopping)

	err := awaitRun(t, done)
	require.ErrorIs(t, err, events.ErrLeaveUncommitted)
	fetches, commits := reader.snapshot()
	assert.Empty(t, commits, "nothing committed")
	assert.Equal(t, 1, fetches, "the second message was never fetched")
	assert.Empty(t, dlq.captured())
	assert.Zero(t, applies.Load())
	assert.Zero(t, count(t, pool, "processed_events"), "no claim was ever made")
}

// A plain context cancel during prepare is the drain case, as for HandleTx: the
// message in flight is finished — apply runs and commits on the live context.
func TestPGPrepared_CancelDuringPrepareStillFinishesTheMessage(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-drain"), userRegisteredMsg(t, "e-next")}}
	entered, release := make(chan struct{}), make(chan struct{})
	disp := pgDisp(pool, reader)
	events.HandlePrepared(disp,
		func(ctx context.Context, _ *usersv1.UserRegistered) (struct{}, error) {
			close(entered)
			<-release
			return struct{}{}, ctx.Err()
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
			return err
		})

	done, cancel := runBackground(disp)
	<-entered
	cancel()
	close(release)

	require.ErrorIs(t, awaitRun(t, done), context.Canceled)
	assert.Equal(t, 1, count(t, pool, "effects"))
	assert.Equal(t, 1, count(t, pool, "processed_events"))
	_, commits := reader.snapshot()
	require.Len(t, commits, 1)
	assert.NoError(t, commits[0])
}

// A panic in either phase is recovered and dead-lettered at once with the panic
// reason — no retry; a panic in apply takes its transaction with it.
func TestPGPrepared_PanicInPrepareIsRecoveredAndDeadLettered(t *testing.T) {
	pool := migratedPool(t)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-panic-prepare")}}
	dlq := &fakeWriter{}
	var prepares atomic.Int32
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			prepares.Add(1)
			panic("boom in prepare")
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Equal(t, int32(1), prepares.Load(), "a panic is not retried")
	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, "handler_panic", string(findTestHeader(dlq.captured()[0], "x-dlq-reason")))
	assert.Zero(t, count(t, pool, "processed_events"))
}

func TestPGPrepared_PanicInApplyIsRecoveredAndRollsBack(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-panic-apply")}}
	dlq := &fakeWriter{}
	disp := newDispWith(events.NewPostgresDeduplicator(pool), reader, dlq)
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) { return struct{}{}, nil },
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			_, _ = tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
			panic("boom in apply")
		})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	require.Len(t, dlq.captured(), 1)
	assert.Equal(t, "handler_panic", string(findTestHeader(dlq.captured()[0], "x-dlq-reason")))
	assert.Zero(t, count(t, pool, "effects"), "the panicking attempt's insert was rolled back")
	assert.Zero(t, count(t, pool, "processed_events"))
	assert.Zero(t, idleInTransaction(t, pool), "and its transaction is closed")
}

// Apply has its own short limit: a hung transaction is cut, rolled back and the
// message retried — while a long prepare does not eat into it.
func TestPGPrepared_ApplyTimeoutCutsAHungTransactionAndPrepareDoesNotStarveIt(t *testing.T) {
	pool := migratedPool(t)
	effectsTable(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-apply-slow")}}
	var prepares, applies atomic.Int32
	disp := pgDisp(pool, reader, events.WithApplyTimeout(150*time.Millisecond), events.WithPrepareTimeout(5*time.Second))
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			prepares.Add(1)
			time.Sleep(300 * time.Millisecond) // longer than the apply limit
			return struct{}{}, nil
		},
		func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered, _ struct{}) error {
			if _, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`); err != nil {
				return err
			}
			if applies.Add(1) == 1 {
				<-ctx.Done() // hung
				return ctx.Err()
			}
			return nil
		})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, applies.Load(), "the first apply was cut at its own limit, not at prepare's")
	assert.EqualValues(t, 2, prepares.Load())
	assert.Equal(t, 1, count(t, pool, "effects"))
}

func TestPGPrepared_PrepareTimeoutFailsTheAttemptAndItIsRetried(t *testing.T) {
	pool := migratedPool(t)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-prepare-slow")}}
	var prepares atomic.Int32
	disp := pgDisp(pool, reader, events.WithPrepareTimeout(100*time.Millisecond))
	events.HandlePrepared(disp,
		func(ctx context.Context, _ *usersv1.UserRegistered) (struct{}, error) {
			if prepares.Add(1) == 1 {
				<-ctx.Done()
				return struct{}{}, ctx.Err()
			}
			return struct{}{}, nil
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, prepares.Load())
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// The defaults come from the environment when no option is given.
func TestPrepared_TimeoutsReadTheirEnvironmentVariables(t *testing.T) {
	t.Setenv("EVENTS_PREPARE_TIMEOUT", "80ms")
	t.Setenv("EVENTS_APPLY_TIMEOUT", "5s")
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-env")}}
	var prepares atomic.Int32
	disp := newDisp(reader, &fakeWriter{}, events.WithRetryWindow(5*time.Second))
	events.HandlePrepared(disp,
		func(ctx context.Context, _ *usersv1.UserRegistered) (struct{}, error) {
			if prepares.Add(1) == 1 {
				<-ctx.Done()
				return struct{}{}, ctx.Err()
			}
			return struct{}{}, nil
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, prepares.Load(), "EVENTS_PREPARE_TIMEOUT=80ms cut the first prepare")
}

func TestPrepared_DefaultsAreSane(t *testing.T) {
	assert.Equal(t, 10*time.Minute, events.DefaultPrepareTimeout)
	assert.Equal(t, 30*time.Second, events.DefaultApplyTimeout)
	assert.Less(t, events.DefaultApplyTimeout, events.DefaultMessageTimeout, "a hung transaction is cut sooner than under HandleTx")
}

// MemoryDeduplicator: apply gets a nil Tx, and a redelivery skips prepare.
func TestPrepared_WorksWithTheMemoryDeduplicatorAndANilTx(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-mem"), userRegisteredMsg(t, "e-mem")}}
	var prepares, applies atomic.Int32
	var gotTx events.Tx
	disp := newDisp(reader, &fakeWriter{})
	events.HandlePrepared(disp,
		func(_ context.Context, m *usersv1.UserRegistered) (string, error) {
			prepares.Add(1)
			return "p:" + m.GetName(), nil
		},
		func(_ context.Context, tx events.Tx, _ *usersv1.UserRegistered, p string) error {
			applies.Add(1)
			gotTx = tx
			assert.Equal(t, "p:Alice", p)
			return nil
		})

	done, cancel := runBackground(disp)
	defer cancel()
	waitCommits(t, reader, 2)
	cancel()
	<-done

	assert.Nil(t, gotTx)
	assert.EqualValues(t, 1, prepares.Load())
	assert.EqualValues(t, 1, applies.Load())
}

func TestHandlePrepared_RefusesNilPhases(t *testing.T) {
	disp := newDisp(&fakeReader{}, &fakeWriter{})
	assert.Panics(t, func() {
		events.HandlePrepared[*usersv1.UserRegistered, struct{}](disp, nil,
			func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })
	})
	assert.Panics(t, func() {
		events.HandlePrepared[*usersv1.UserRegistered, struct{}](disp,
			func(context.Context, *usersv1.UserRegistered) (struct{}, error) { return struct{}{}, nil }, nil)
	})
}

// The retry window starts at the first FAILURE: a prepare that runs longer than
// the window and then fails still gets its retries (counted from the attempt's
// start it would be dead-lettered at once).
func TestPrepared_TheRetryWindowStartsAtTheFirstFailureNotTheFirstAttempt(t *testing.T) {
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-window")}}
	dlq := &fakeWriter{}
	var prepares atomic.Int32
	disp := newDisp(reader, dlq, events.WithRetryWindow(200*time.Millisecond))
	events.HandlePrepared(disp,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) {
			if prepares.Add(1) == 1 {
				time.Sleep(400 * time.Millisecond) // longer than the whole window
				return struct{}{}, errors.New("ffmpeg exited 1")
			}
			return struct{}{}, nil
		},
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, prepares.Load(), "retried after the slow failure")
	assert.Empty(t, dlq.captured())
}
