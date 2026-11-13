package events_test

import (
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"

	"github.com/STECH-Super-App/go-common/pkg/events"
)

// SL-01: the dispatcher commits the source offset after a "successful" DLQ
// write. On a RequireNone or Async writer that success is a lie, so the
// message is lost. NewDispatcher must refuse such a writer for the DLQ and the
// retry tier.

func TestNewDispatcher_RefusesUnsafeDLQWriter(t *testing.T) {
	unsafe := map[string]*kafka.Writer{
		"zero value (RequireNone)": {Addr: kafka.TCP("localhost:9092"), Topic: "x.dlq.g"},
		"Async": {
			Addr: kafka.TCP("localhost:9092"), Topic: "x.dlq.g",
			RequiredAcks: kafka.RequireAll, Async: true,
		},
	}
	for name, w := range unsafe {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { events.NewDispatcher(&fakeReader{}, w) })
			assert.Panics(t, func() {
				events.NewDispatcher(&fakeReader{}, &fakeWriter{}, events.WithRetry(w))
			})
		})
	}
}

func TestNewDispatcher_AcceptsSafeDLQWriter(t *testing.T) {
	w := &kafka.Writer{Addr: kafka.TCP("localhost:9092"), Topic: "x.dlq.g", RequiredAcks: kafka.RequireAll}
	assert.NotPanics(t, func() { events.NewDispatcher(&fakeReader{}, w, events.WithRetry(w)) })
}
