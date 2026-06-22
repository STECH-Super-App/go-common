package outbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// claimStore models the processed_outbox_messages table's primary-key
// constraint on event_id. Concurrent claims for the same id are serialized
// under the mutex, so exactly one caller observes the insert as taken — this
// is the behaviour the Postgres PK + INSERT ... ON CONFLICT DO NOTHING gives us.
type claimStore struct {
	mu      sync.Mutex
	claimed map[string]bool
	execErr error // when set, every Exec fails (simulates a DB error)
}

func newClaimStore() *claimStore {
	return &claimStore{claimed: map[string]bool{}}
}

// claim returns the number of rows the INSERT took: 1 the first time an id is
// seen, 0 on every conflict afterwards.
func (s *claimStore) claim(eventID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.execErr != nil {
		return 0, s.execErr
	}
	if s.claimed[eventID] {
		return 0, nil
	}
	s.claimed[eventID] = true
	return 1, nil
}

// fakeTx is a pgx.Tx that only implements Exec (the one method the
// deduplicator's claim path uses); every other method panics so an
// accidental new dependency is caught loudly. It extracts the event_id from
// the claim INSERT's first argument and defers to the shared claimStore.
type fakeTx struct {
	store *claimStore
}

func (t *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !strings.HasPrefix(sql, "INSERT INTO processed_outbox_messages") {
		return pgconn.CommandTag{}, errors.New("fakeTx: unexpected sql: " + sql)
	}
	eventID, _ := args[0].(string)
	n, err := t.store.claim(eventID)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if n == 1 {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 0"), nil
}

// Remaining pgx.Tx surface — unused by the deduplicator, present only to
// satisfy the interface.
func (t *fakeTx) Begin(context.Context) (pgx.Tx, error) { panic("fakeTx.Begin unused") }
func (t *fakeTx) Commit(context.Context) error          { panic("fakeTx.Commit unused") }
func (t *fakeTx) Rollback(context.Context) error        { panic("fakeTx.Rollback unused") }
func (t *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("fakeTx.CopyFrom unused")
}
func (t *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("fakeTx.SendBatch unused")
}
func (t *fakeTx) LargeObjects() pgx.LargeObjects { panic("fakeTx.LargeObjects unused") }
func (t *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("fakeTx.Prepare unused")
}
func (t *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakeTx.Query unused")
}
func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { panic("fakeTx.QueryRow unused") }
func (t *fakeTx) Conn() *pgx.Conn                                  { panic("fakeTx.Conn unused") }

// Compile-time assertion that fakeTx fully satisfies pgx.Tx.
var _ pgx.Tx = (*fakeTx)(nil)

func TestDeduplicator_processInTx(t *testing.T) {
	sentinel := errors.New("handler exploded")
	execErr := errors.New("connection reset")

	tests := []struct {
		name string
		// seedClaimed pre-claims the event_id so this delivery is a duplicate.
		seedClaimed bool
		execErr     error
		fnErr       error
		wantFnRuns  int
		wantErrIs   error
	}{
		{
			name:        "first-time delivery wins claim and runs handler",
			seedClaimed: false,
			wantFnRuns:  1,
		},
		{
			name:        "already-processed delivery skips handler",
			seedClaimed: true,
			wantFnRuns:  0,
		},
		{
			name:       "claim DB error propagates and handler does not run",
			execErr:    execErr,
			wantFnRuns: 0,
			wantErrIs:  execErr,
		},
		{
			name:       "handler error propagates from a won claim",
			fnErr:      sentinel,
			wantFnRuns: 1,
			wantErrIs:  sentinel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newClaimStore()
			store.execErr = tc.execErr
			if tc.seedClaimed {
				store.claimed["evt-1"] = true
			}

			d := &Deduplicator{}
			tx := &fakeTx{store: store}

			fnRuns := 0
			err := d.processInTx(context.Background(), tx, "evt-1", func() error {
				fnRuns++
				return tc.fnErr
			})

			if fnRuns != tc.wantFnRuns {
				t.Errorf("fn ran %d times, want %d", fnRuns, tc.wantFnRuns)
			}
			if tc.wantErrIs == nil {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
			} else if !errors.Is(err, tc.wantErrIs) {
				t.Errorf("err = %v, want errors.Is(%v)", err, tc.wantErrIs)
			}
		})
	}
}

// TestDeduplicator_concurrentFirstTimeDeliveries is the H25 regression test:
// two pods receiving the same event_id for the FIRST time must run the handler
// exactly once between them. The old SELECT ... FOR UPDATE-then-INSERT order
// let both pass the check and both run fn; the insert-first claim makes the
// claim atomic so only the winner runs fn.
func TestDeduplicator_concurrentFirstTimeDeliveries(t *testing.T) {
	const deliverers = 16

	store := newClaimStore()
	// Force a maximally-contended race: every deliverer blocks on the gate
	// until they have all spun up, then they all attempt the claim at once.
	// The claimStore mutex models the serialization the Postgres PK
	// constraint imposes on concurrent inserts for the same key.
	var ready sync.WaitGroup
	ready.Add(deliverers)
	gate := make(chan struct{})

	d := &Deduplicator{}

	var handlerRuns int64
	var wg sync.WaitGroup
	wg.Add(deliverers)

	for i := 0; i < deliverers; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			<-gate // all deliverers start the claim together
			tx := &fakeTx{store: store}
			err := d.processInTx(context.Background(), tx, "same-event", func() error {
				atomic.AddInt64(&handlerRuns, 1)
				return nil
			})
			if err != nil {
				t.Errorf("processInTx err = %v, want nil", err)
			}
		}()
	}

	ready.Wait()
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt64(&handlerRuns); got != 1 {
		t.Fatalf("handler ran %d times for one event_id, want exactly 1", got)
	}
}

// TestDeduplicator_Process_emptyEventIDRunsHandlerWithoutDedup covers the
// no-event_id-header escape hatch in Process (which the per-tx helper never
// sees): with no id there is nothing to dedup, so fn runs directly.
func TestDeduplicator_Process_emptyEventIDRunsHandlerWithoutDedup(t *testing.T) {
	d := &Deduplicator{} // nil pool — must never be touched on this path

	ran := false
	err := d.Process(context.Background(), "", func() error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("Process err = %v, want nil", err)
	}
	if !ran {
		t.Error("fn did not run for empty event_id")
	}
}

// TestDeduplicator_Process_emptyEventIDPropagatesHandlerError confirms the
// empty-id path still surfaces handler failures unchanged.
func TestDeduplicator_Process_emptyEventIDPropagatesHandlerError(t *testing.T) {
	d := &Deduplicator{}
	sentinel := errors.New("boom")

	err := d.Process(context.Background(), "", func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want errors.Is(%v)", err, sentinel)
	}
}
