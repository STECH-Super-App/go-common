package events

import (
	"testing"
	"time"
)

// TestDefaultReaderMaxWait pins the fleet's fetch long-poll: lifecycle's
// shutdown budget assumes one Kafka CloserGroup costs at most ~2 s. Raising it
// back towards kafka-go's 10 s default is a fleet-wide shutdown regression.
func TestDefaultReaderMaxWait(t *testing.T) {
	if DefaultReaderMaxWait != 2*time.Second {
		t.Fatalf("DefaultReaderMaxWait = %v, want 2s", DefaultReaderMaxWait)
	}
}
