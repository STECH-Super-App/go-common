package events

import (
	"testing"
	"time"
)

// The fetch backoff doubles from 100 ms and caps at 5 s.
func TestNextFetchBackoff_DoublesAndCaps(t *testing.T) {
	want := []time.Duration{
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3200 * time.Millisecond,
		5 * time.Second,
		5 * time.Second,
	}
	got := fetchBackoffInitial
	for i, w := range want {
		got = nextFetchBackoff(got)
		if got != w {
			t.Fatalf("step %d: got %v, want %v", i, got, w)
		}
	}
}
