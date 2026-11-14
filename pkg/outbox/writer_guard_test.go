package outbox

import (
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// SL-01: the relay marks a row sent as soon as WriteMessages returns nil.
// kafka-go's zero value is RequireNone and Async hands the batch to a
// background goroutine, so under either the row is marked sent before any
// broker has answered. The constructors must refuse such a writer.

func unsafeWriters() map[string]*kafka.Writer {
	return map[string]*kafka.Writer{
		"zero value (RequireNone)": {Addr: kafka.TCP("localhost:9092")},
		"explicit RequireNone": {
			Addr: kafka.TCP("localhost:9092"), RequiredAcks: kafka.RequireNone,
		},
		"Async with RequireAll": {
			Addr: kafka.TCP("localhost:9092"), RequiredAcks: kafka.RequireAll, Async: true,
		},
	}
}

func TestNew_RefusesUnsafeWriter(t *testing.T) {
	for name, w := range unsafeWriters() {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() {
				New(nil, w, zap.NewNop(), &Config{}, "topic")
			})
		})
	}
}

func TestNewRelay_RefusesUnsafeWriter(t *testing.T) {
	for name, w := range unsafeWriters() {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() {
				NewRelay(nil, w, zap.NewNop(), RelayConfig{PollInterval: time.Second, BatchSize: 1})
			})
		})
	}
}

func TestNew_AcceptsSafeWriter(t *testing.T) {
	w := &kafka.Writer{Addr: kafka.TCP("localhost:9092"), RequiredAcks: kafka.RequireAll, Balancer: &kafka.Hash{}}
	assert.NotPanics(t, func() {
		New(nil, w, zap.NewNop(), &Config{}, "topic")
	})
}

// R11 / Critical Rule 13: a domain-event writer must keep a key on its
// partition. nil is round-robin and LeastBytes ignores the key.
func TestNewAndNewRelay_RefuseAWriterThatIgnoresTheKey(t *testing.T) {
	for name, b := range map[string]kafka.Balancer{
		"nil (round-robin)": nil,
		"RoundRobin":        &kafka.RoundRobin{},
		"LeastBytes":        &kafka.LeastBytes{},
	} {
		w := &kafka.Writer{Addr: kafka.TCP("localhost:9092"), RequiredAcks: kafka.RequireAll, Balancer: b}
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { New(nil, w, zap.NewNop(), &Config{}, "topic") })
			assert.Panics(t, func() { NewRelay(nil, w, zap.NewNop(), RelayConfig{}) })
		})
	}
}
