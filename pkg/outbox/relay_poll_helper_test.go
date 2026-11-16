package outbox

import (
	"context"
	"sync"
)

// pollResult is the outcome of one synchronous pass over every pending topic.
type pollResult struct {
	sent   int  // rows marked sent across all topics
	failed bool // the topic list, a claim, a write or a mark failed
	more   bool // a topic returned a full batch and wrote it cleanly
}

// poll is a test helper: one tick of the relay's scheduler, then wait for every
// forward it started. Run never waits like this — that barrier was R3 — but a
// test that asserts on one pass needs the outcome of all of it.
func (r *Relay) poll(ctx context.Context) pollResult {
	var (
		mu  sync.Mutex
		out pollResult
	)
	_, err := r.tick(ctx, func(o topicOutcome) {
		mu.Lock()
		defer mu.Unlock()
		out.sent += o.res.Sent
		out.failed = out.failed || o.err != nil
		out.more = out.more || o.more
	})
	r.wg.Wait()
	if err != nil && ctx.Err() == nil {
		out.failed = true
	}
	return out
}
