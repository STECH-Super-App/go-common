package outbox

// The size limit is exact (R10): it measures what the relay will send, which is
// Postgres' jsonb::text, not the compact JSON the publisher handed in.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
)

func manyFieldsPayload(n int) []byte {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`"f%03d":%d`, i, i)
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

func TestPG_InsertTxLimitedMeasuresJsonbTextExactly(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()

	msg := func() *Message {
		return &Message{
			ID: newID(), AggregateType: "a", AggregateID: "agg", EventType: "events.x.v1.X", Topic: "t.events",
			Key: "agg", Payload: manyFieldsPayload(200), Headers: map[string]string{"event_id": "e"},
			Status: StatusPending, CreatedAt: time.Now().UTC(),
		}
	}
	var jsonbLen int
	require.NoError(t, pool.QueryRow(ctx, `SELECT octet_length($1::jsonb::text)`, msg().Payload).Scan(&jsonbLen))
	m := msg()
	compact := m.compactWireSize()
	exact := jsonbLen + m.nonPayloadSize()
	require.Greater(t, exact, compact+150, "jsonb::text is larger than the compact JSON (a space after every , and :)")

	insert := func(limit int) error {
		return RunInTx(ctx, pool, func(tx Tx) error { return store.InsertTxLimited(ctx, tx, msg(), limit) })
	}
	rows := func() (n int) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages`).Scan(&n))
		return n
	}

	// Between the compact size and the real one: the old guard let this through.
	err := insert(compact + 100)
	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "typed error, got %v", err)
	assert.Equal(t, ReasonEventTooLarge, ae.Reason)
	assert.Equal(t, 422, ae.Code)
	assert.EqualValues(t, exact, ae.Params["size"], "the error names the size that would have been sent")
	assert.Zero(t, rows(), "an oversized row is not inserted")

	require.Error(t, insert(exact-1), "one byte over")
	assert.Zero(t, rows())
	require.NoError(t, insert(exact), "exactly at the limit")
	assert.Equal(t, 1, rows())
	require.NoError(t, insert(0), "no limit")
	assert.Equal(t, 2, rows())

	// The stored payload really is that many bytes when the relay reads it.
	var sent int
	require.NoError(t, pool.QueryRow(ctx, `SELECT octet_length(payload::text) FROM outbox_messages LIMIT 1`).Scan(&sent))
	assert.Equal(t, jsonbLen, sent)
}

// A caller that swallows the error and commits anyway leaves no oversized row.
func TestPG_OversizedRowIsNeverInsertedEvenIfTheCallerCommits(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	m := &Message{ID: newID(), AggregateType: "a", AggregateID: "agg", EventType: "e", Topic: "t", Key: "k",
		Payload: manyFieldsPayload(100), Headers: map[string]string{}, Status: StatusPending, CreatedAt: time.Now().UTC()}
	require.Error(t, store.InsertTxLimited(ctx, tx, m, 50))
	require.NoError(t, tx.Commit(ctx), "the transaction is still usable")

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages`).Scan(&n))
	assert.Zero(t, n)
}

// Through the publisher, with the real store: an event whose compact JSON fits
// the limit and whose jsonb text does not is refused with the typed 422 and
// leaves nothing behind.
func TestPG_PublishProtoRefusesWhatWouldBeOverTheLimitAsJsonb(t *testing.T) {
	pool, store := migratedDB(t)
	ctx := context.Background()

	fields := map[string]any{}
	for i := 0; i < 300; i++ {
		fields[fmt.Sprintf("f%03d", i)] = float64(i)
	}
	s, err := structpb.NewStruct(fields)
	require.NoError(t, err)
	opts := PublishProtoOptions{AggregateType: "a", AggregateID: "x", Message: s}

	// The two sizes of one event: compact (what the in-memory store keeps) and as
	// Postgres will send it. The occurred_at header varies by a few bytes between
	// calls, which the 200+ byte gap between the two swallows.
	mem := NewTestStore()
	require.NoError(t, NewPublisher(mem, "t.events").PublishProto(ctx, nil, opts))
	compact := mem.Messages[0].compactWireSize()

	probe := NewPublisher(store, "t.events")
	require.NoError(t, RunInTx(ctx, pool, func(tx Tx) error { return probe.PublishProto(ctx, tx, opts) }))
	var jsonbText int
	var headersJSON []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT octet_length(payload::text), headers FROM outbox_messages`).Scan(&jsonbText, &headersJSON))
	row := &Message{Key: "x"}
	require.NoError(t, json.Unmarshal(headersJSON, &row.Headers))
	exact := jsonbText + row.nonPayloadSize()
	require.Greater(t, exact, compact+200)
	_, err = pool.Exec(ctx, `DELETE FROM outbox_messages`)
	require.NoError(t, err)

	limit := (compact + exact) / 2
	pub := NewPublisher(store, "t.events", WithMaxMessageBytes(limit))
	err = RunInTx(ctx, pool, func(tx Tx) error { return pub.PublishProto(ctx, tx, opts) })

	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "typed error, got %v", err)
	assert.Equal(t, ReasonEventTooLarge, ae.Reason)
	assert.Greater(t, ae.Params["size"], limit)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages`).Scan(&n))
	assert.Zero(t, n)
}
