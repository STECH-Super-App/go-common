package events_test

// The deduplicator and the transaction-form handler against a REAL Postgres
// (SL-04, SL-31, SL-09). Kafka is a fake reader: the properties under test are
// the database's.

import (
	"context"
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
	"github.com/STECH-Super-App/go-common/pkg/internal/testpg"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, pool := testpg.New(t)
	require.NoError(t, outbox.Migrate(url))
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

// refuseCommitsWhileFlagged makes every COMMIT that inserted a processed_events
// row fail while refuse_flag has a row: the deferred constraint trigger raises
// at commit time, which is how a real commit failure (a serialization failure, a
// lost connection after the last statement) looks to the dispatcher.
func refuseCommitsWhileFlagged(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE effects (n int);
		CREATE TABLE refuse_flag (x int);
		INSERT INTO refuse_flag VALUES (1);
		CREATE FUNCTION refuse_commit() RETURNS trigger AS $$
		BEGIN
			IF EXISTS (SELECT 1 FROM refuse_flag) THEN RAISE EXCEPTION 'commit refused'; END IF;
			RETURN NULL;
		END $$ LANGUAGE plpgsql;
		CREATE CONSTRAINT TRIGGER refuse AFTER INSERT ON processed_events
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refuse_commit();`)
	require.NoError(t, err)
}

func clearFlag(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `DELETE FROM refuse_flag`)
	require.NoError(t, err)
}

func pgDisp(pool *pgxpool.Pool, reader events.Reader, opts ...events.DispatcherOption) *events.Dispatcher {
	return newDispWith(events.NewPostgresDeduplicator(pool), reader, &fakeWriter{},
		append([]events.DispatcherOption{events.WithRetryWindow(5 * time.Second), events.WithGroup("g")}, opts...)...)
}

// SL-04: the handler's writes and the "processed" mark are ONE transaction. The
// commit fails once: on the release 1 tree the handler's write (made through the
// pool, the only thing it could do) stayed, and the retry wrote it again.
func TestPG_HandleTxEffectAndProcessedMarkAreAtomic(t *testing.T) {
	pool := migratedPool(t)
	refuseCommitsWhileFlagged(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-atomic")}}
	var calls atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandleTx(disp, func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered) error {
		if calls.Add(1) == 2 {
			// The first attempt's commit was refused: neither its effect nor its
			// claim may be visible now.
			assert.Zero(t, count(t, pool, "effects"), "the failed attempt's write rolled back with its claim")
			assert.Zero(t, count(t, pool, "processed_events"))
			clearFlag(t, pool)
		}
		_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
		return err
	})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, calls.Load(), "one refused commit, one retry")
	assert.Equal(t, 1, count(t, pool, "effects"), "the effect happened exactly once")
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// The coexistence promise: the old handler form still deduplicates, but it is
// not atomic — which is exactly SL-04, kept visible here so nobody mistakes the
// legacy form for the fixed one.
func TestPG_LegacyHandleStillDeduplicatesButIsNotAtomic(t *testing.T) {
	pool := migratedPool(t)
	refuseCommitsWhileFlagged(t, pool)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-legacy"), userRegisteredMsg(t, "e-legacy")}}
	var calls atomic.Int32
	disp := pgDisp(pool, reader)
	events.Handle(disp, func(ctx context.Context, _ *usersv1.UserRegistered) error {
		if calls.Add(1) == 2 {
			clearFlag(t, pool)
		}
		_, err := pool.Exec(ctx, `INSERT INTO effects VALUES (1)`) // a write through the pool
		return err
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	require.Eventually(t, func() bool { _, c := reader.snapshot(); return len(c) == 2 }, 3*time.Second, 5*time.Millisecond)
	cancel()
	<-done

	assert.EqualValues(t, 2, calls.Load(), "the redelivery after the second message was a dedup hit")
	assert.Equal(t, 2, count(t, pool, "effects"), "the refused commit left the first write in place and the retry wrote it again")
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// SL-31: the claim is per consumer. Two groups handle the same event id; one
// group handles it once however often it is delivered.
func TestPG_DedupIsPerConsumerAndOncePerEvent(t *testing.T) {
	pool := migratedPool(t)
	var a, b atomic.Int32
	run := func(group string, n *atomic.Int32, deliveries int) {
		msgs := make([]kafka.Message, deliveries)
		for i := range msgs {
			msgs[i] = userRegisteredMsg(t, "e-shared")
		}
		reader := &greedyReader{msgs: msgs}
		disp := pgDisp(pool, reader, events.WithGroup(group))
		events.HandleTx(disp, func(context.Context, events.Tx, *usersv1.UserRegistered) error { n.Add(1); return nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- disp.Run(ctx) }()
		require.Eventually(t, func() bool { _, c := reader.snapshot(); return len(c) == deliveries }, 3*time.Second, 5*time.Millisecond)
		cancel()
		<-done
	}

	run("group-a", &a, 3)
	run("group-b", &b, 1)

	assert.EqualValues(t, 1, a.Load(), "three deliveries, one execution")
	assert.EqualValues(t, 1, b.Load(), "another consumer is not stopped by group-a's claim")
	assert.Equal(t, 2, count(t, pool, "processed_events"))
}

// An event the previous deduplicator recorded is not run again after the
// upgrade.
func TestPG_DedupRecognisesClaimsOfThePreviousDeduplicator(t *testing.T) {
	pool := migratedPool(t)
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(), `INSERT INTO processed_outbox_messages (event_id) VALUES ($1)`, id)
	require.NoError(t, err)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, id)}}
	var calls atomic.Int32
	disp := pgDisp(pool, reader)
	events.HandleTx(disp, func(context.Context, events.Tx, *usersv1.UserRegistered) error { calls.Add(1); return nil })

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.Zero(t, calls.Load(), "already processed before the upgrade")
	assert.Equal(t, 1, count(t, pool, "processed_events"), "and recorded now, so the next redelivery stops at the first INSERT")
}

// Two pods get the same event at once: the claim INSERT is the compare-and-set,
// the second waits on the primary key and then sees it processed.
func TestPG_ConcurrentDeliveriesRunTheHandlerOnce(t *testing.T) {
	pool := migratedPool(t)
	var runs atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-race")}}
		disp := pgDisp(pool, reader)
		events.HandleTx(disp, func(context.Context, events.Tx, *usersv1.UserRegistered) error {
			runs.Add(1)
			time.Sleep(100 * time.Millisecond) // hold the claim so the others collide with it
			return nil
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

	assert.EqualValues(t, 1, runs.Load())
	assert.Equal(t, 1, count(t, pool, "processed_events"))
}

// A shutdown that arrives mid-handler still commits the handler's transaction
// and its claim: Run's detached context keeps them alive.
func TestPG_ShutdownMidHandlerStillCommitsTheTransaction(t *testing.T) {
	pool := migratedPool(t)
	_, err := pool.Exec(context.Background(), `CREATE TABLE effects (n int)`)
	require.NoError(t, err)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-drain"), userRegisteredMsg(t, "e-next")}}
	entered, release := make(chan struct{}), make(chan struct{})
	disp := pgDisp(pool, reader)
	events.HandleTx(disp, func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered) error {
		close(entered)
		<-release
		_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
		return err
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	<-entered
	cancel()
	close(release)

	require.ErrorIs(t, awaitRun(t, done), context.Canceled)
	assert.Equal(t, 1, count(t, pool, "effects"))
	assert.Equal(t, 1, count(t, pool, "processed_events"), "the claim committed on the live context")
	_, commits := reader.snapshot()
	require.Len(t, commits, 1)
	assert.NoError(t, commits[0])
}

// The per-message limit cuts a stuck handler, and the transaction goes with it:
// the stuck attempt's write is rolled back and the retry starts clean.
func TestPG_MessageTimeoutRollsTheTransactionBack(t *testing.T) {
	pool := migratedPool(t)
	_, err := pool.Exec(context.Background(), `CREATE TABLE effects (n int)`)
	require.NoError(t, err)
	reader := &greedyReader{msgs: []kafka.Message{userRegisteredMsg(t, "e-slow")}}
	var calls atomic.Int32
	disp := pgDisp(pool, reader, events.WithMessageTimeout(150*time.Millisecond))
	events.HandleTx(disp, func(ctx context.Context, tx events.Tx, _ *usersv1.UserRegistered) error {
		if _, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`); err != nil {
			return err
		}
		if calls.Add(1) == 1 {
			<-ctx.Done() // stuck on a dead dependency
			return ctx.Err()
		}
		return nil
	})

	require.ErrorIs(t, runOne(t, disp, reader), context.Canceled)

	assert.EqualValues(t, 2, calls.Load())
	assert.Equal(t, 1, count(t, pool, "effects"), "the cut attempt's insert did not survive")
}

func TestNewDispatcher_RequiresADeduplicator(t *testing.T) {
	assert.Panics(t, func() { events.NewDispatcher(&fakeReader{}, &fakeWriter{}, nil, events.WithGroup("g")) })
	assert.Panics(t, func() { events.NewPostgresDeduplicator(nil) })
	assert.NotPanics(t, func() {
		events.NewDispatcher(&fakeReader{}, &fakeWriter{}, events.NewMemoryDeduplicator(), events.WithGroup("g"))
	})
}

func TestPG_DeduplicatorReadyFailsWhenTheTableIsMissing(t *testing.T) {
	pool := migratedPool(t)
	d := events.NewPostgresDeduplicator(pool)
	require.NoError(t, d.Ready(context.Background()))

	_, err := pool.Exec(context.Background(), `DROP TABLE processed_events`)
	require.NoError(t, err)
	assert.Error(t, d.Ready(context.Background()), "a service whose migration did not run must not start consuming")
}
