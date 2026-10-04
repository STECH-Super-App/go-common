package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/lifecycle"
)

// signalShutdown runs app and triggers a clean signal shutdown at once.
func signalShutdown(t *testing.T, app *lifecycle.App, within time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, app)
	cancel()
	return waitResult(t, done, within)
}

// The members of a group run concurrently: each one waits until every member
// has started, which only a concurrent run can satisfy. Run sequentially, the
// first member would give up after a second and fail the shutdown.
func TestCloserGroup_MembersRunConcurrently(t *testing.T) {
	const n = 3
	var arrived sync.WaitGroup
	arrived.Add(n)
	allArrived := make(chan struct{})
	go func() { arrived.Wait(); close(allArrived) }()

	members := make([]lifecycle.NamedCloser, 0, n)
	for i := range n {
		members = append(members, lifecycle.NamedCloser{
			Name: fmt.Sprintf("reader-%d", i),
			Close: func(context.Context) error {
				arrived.Done()
				select {
				case <-allArrived:
					return nil
				case <-time.After(time.Second):
					return errors.New("siblings never started: members ran sequentially")
				}
			},
		})
	}
	app := lifecycle.New(zap.NewNop())
	app.CloserGroup("kafka", members...)

	require.NoError(t, signalShutdown(t, app, 5*time.Second))
}

// The group step costs the slowest member, not the sum of the members — the
// reason it exists (kafka-go Reader.Close waits out a fetch long-poll).
func TestCloserGroup_DurationIsTheMaxNotTheSum(t *testing.T) {
	const each = 200 * time.Millisecond
	slow := func(context.Context) error { time.Sleep(each); return nil }
	app := lifecycle.New(zap.NewNop())
	app.CloserGroup("kafka",
		lifecycle.NamedCloser{Name: "reader-a", Close: slow},
		lifecycle.NamedCloser{Name: "reader-b", Close: slow},
		lifecycle.NamedCloser{Name: "writer", Close: slow},
	)

	began := time.Now()
	require.NoError(t, signalShutdown(t, app, 5*time.Second))
	elapsed := time.Since(began)
	assert.GreaterOrEqual(t, elapsed, each)
	assert.Less(t, elapsed, 2*each, "three %v members took %v: they did not overlap", each, elapsed)
}

// A failing or panicking member is reported under its own name and does not
// stop its siblings; a clean signal shutdown with a member error is unclean.
func TestCloserGroup_MemberFailuresAreReportedAndSiblingsFinish(t *testing.T) {
	closeErr := errors.New("close reader: broker gone")
	j := &journal{}
	app := lifecycle.New(zap.NewNop())
	app.CloserGroup("kafka",
		lifecycle.NamedCloser{Name: "reader-a", Close: func(context.Context) error { return closeErr }},
		lifecycle.NamedCloser{Name: "reader-b", Close: func(context.Context) error { panic("closed twice") }},
		lifecycle.NamedCloser{Name: "writer", Close: func(context.Context) error {
			time.Sleep(50 * time.Millisecond) // finishes after both siblings failed
			j.add("writer")
			return nil
		}},
	)
	app.Closer("postgres", func(context.Context) error { j.add("postgres"); return nil })

	err := signalShutdown(t, app, 5*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrCloserFailed)
	require.ErrorIs(t, err, closeErr)
	require.ErrorIs(t, err, lifecycle.ErrPanic)
	assert.Contains(t, err.Error(), `"kafka/reader-a"`)
	assert.Contains(t, err.Error(), `"kafka/reader-b"`)
	assert.Equal(t, []string{"writer", "postgres"}, j.list())
}

// The budget ending mid-group reports exactly the unfinished members as timed
// out, Run still returns, and the closers after the group are skipped.
func TestCloserGroup_BudgetExpiryMidGroup(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	logger, logs := observedLogger()
	app := lifecycle.New(logger, lifecycle.WithShutdownBudget(200*time.Millisecond))
	app.CloserGroup("kafka",
		lifecycle.NamedCloser{Name: "fast", Close: func(context.Context) error { return nil }},
		lifecycle.NamedCloser{Name: "deaf", Close: func(context.Context) error { <-block; return nil }},
		lifecycle.NamedCloser{Name: "also-deaf", Close: func(context.Context) error { <-block; return nil }},
	)
	app.Closer("postgres", func(context.Context) error { return nil })

	err := signalShutdown(t, app, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
	assertShutdownComplete(t, logs, "signal",
		[]string{"closer:kafka/deaf", "closer:kafka/also-deaf", "closer:postgres"})
}

// A group whose turn comes after the budget is spent is skipped, and every
// member is reported, not just the group.
func TestCloserGroup_SkippedGroupReportsEveryMember(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	logger, logs := observedLogger()
	app := lifecycle.New(logger, lifecycle.WithShutdownBudget(100*time.Millisecond))
	app.Worker("deaf", func(context.Context) error { <-block; return nil })
	ran := make(chan string, 2)
	record := func(name string) func(context.Context) error {
		return func(context.Context) error { ran <- name; return nil }
	}
	app.CloserGroup("kafka",
		lifecycle.NamedCloser{Name: "reader", Close: record("reader")},
		lifecycle.NamedCloser{Name: "writer", Close: record("writer")},
	)

	err := signalShutdown(t, app, 2*time.Second)
	require.ErrorIs(t, err, lifecycle.ErrShutdownTimeout)
	assert.Empty(t, ran, "no member may start once the budget is spent")
	assertShutdownComplete(t, logs, "signal",
		[]string{"worker:deaf", "closer:kafka/reader", "closer:kafka/writer"})
}

// The group is ONE step at its registration position: the closer before it
// has finished when its members start, and the closer after it starts only
// once every member has finished.
func TestCloserGroup_SitsAtItsRegistrationPosition(t *testing.T) {
	j := &journal{}
	app := lifecycle.New(zap.NewNop())
	app.Closer("before", func(context.Context) error {
		time.Sleep(30 * time.Millisecond)
		j.add("before:done")
		return nil
	})
	member := func(name string, d time.Duration) lifecycle.NamedCloser {
		return lifecycle.NamedCloser{Name: name, Close: func(context.Context) error {
			j.add(name + ":start")
			time.Sleep(d)
			j.add(name + ":done")
			return nil
		}}
	}
	app.CloserGroup("kafka", member("reader", 60*time.Millisecond), member("writer", 10*time.Millisecond))
	app.Closer("after", func(context.Context) error { j.add("after"); return nil })

	require.NoError(t, signalShutdown(t, app, 5*time.Second))
	events := j.list()
	require.Len(t, events, 6)
	assert.Equal(t, "before:done", events[0])
	assert.ElementsMatch(t,
		[]string{"reader:start", "reader:done", "writer:start", "writer:done"}, events[1:5])
	assert.Equal(t, "after", events[5])
}

// An empty group is a no-op, not a failure.
func TestCloserGroup_EmptyGroupIsANoOp(t *testing.T) {
	app := lifecycle.New(zap.NewNop())
	app.CloserGroup("kafka")
	require.NoError(t, signalShutdown(t, app, 2*time.Second))
}
