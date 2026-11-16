package events

import (
	"context"
	"io"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReader struct{}

func (stubReader) FetchMessage(context.Context) (kafka.Message, error) {
	return kafka.Message{}, io.EOF
}
func (stubReader) CommitMessages(context.Context, ...kafka.Message) error { return nil }

type stubWriter struct{}

func (stubWriter) WriteMessages(context.Context, ...kafka.Message) error { return nil }

// R6: the group names the deduplicator's claims. Two dispatchers that both
// forgot WithGroup used to share the claims of the group "unknown" and dropped
// each other's events as already processed.
func TestNewDispatcher_PanicsWithoutAGroup(t *testing.T) {
	assert.Panics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, NewMemoryDeduplicator()) },
		"a reader that does not expose a GroupID and no WithGroup")
	assert.Panics(t, func() { NewDispatcher(nil, stubWriter{}, NewMemoryDeduplicator()) })
	assert.Panics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, NewMemoryDeduplicator(), WithGroup("")) },
		"an empty WithGroup is not a group")
	assert.NotPanics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, NewMemoryDeduplicator(), WithGroup("g")) })
}

func TestNewDispatcher_TakesTheGroupFromTheReader(t *testing.T) {
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, GroupID: "orders-consumer", Topic: "order.events"})
	defer func() { _ = r.Close() }()

	d := NewDispatcher(r, stubWriter{}, NewMemoryDeduplicator())
	assert.Equal(t, "orders-consumer", d.group)

	d = NewDispatcher(r, stubWriter{}, NewMemoryDeduplicator(), WithGroup("explicit"))
	assert.Equal(t, "explicit", d.group, "an explicit WithGroup wins")

	noGroup := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "order.events", Partition: 0})
	defer func() { _ = noGroup.Close() }()
	assert.Panics(t, func() { NewDispatcher(noGroup, stubWriter{}, NewMemoryDeduplicator()) },
		"a reader with no GroupID gives no group")

	var nilReader *kafka.Reader
	assert.Panics(t, func() { NewDispatcher(nilReader, stubWriter{}, NewMemoryDeduplicator()) },
		"a typed-nil reader is not asked for its configuration")
}

// A (*PostgresDeduplicator)(nil) is a non-nil interface value: `dedup == nil`
// passed it and the first message crashed the consumer.
func TestNewDispatcher_RefusesATypedNilDeduplicator(t *testing.T) {
	var pg *PostgresDeduplicator
	var mem *MemoryDeduplicator
	asInterface := func(d Deduplicator) bool { return d != nil }
	require.True(t, asInterface(pg), "the premise: the interface value is not nil")

	assert.Panics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, pg, WithGroup("g")) })
	assert.Panics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, mem, WithGroup("g")) })
	assert.Panics(t, func() { NewDispatcher(stubReader{}, stubWriter{}, nil, WithGroup("g")) })
}
