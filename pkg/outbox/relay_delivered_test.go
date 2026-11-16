package outbox

import (
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
)

func TestDeliveredIDs(t *testing.T) {
	m := func(id, key string) *Message { return &Message{ID: id, Key: key} }
	boom := errors.New("boom")
	batch := []*Message{m("1", "A"), m("2", "B"), m("3", "A"), m("4", "C")}

	cases := []struct {
		name string
		err  error
		want []string
	}{
		{"all written", nil, []string{"1", "2", "3", "4"}},
		{"whole call failed", boom, nil},
		{"wrapped whole-call failure", errors.Join(boom), nil},
		{"per-message: only 2 failed", kafka.WriteErrors{nil, boom, nil, nil}, []string{"1", "3", "4"}},
		{"per-message: 1 failed holds back 3 (same key A)", kafka.WriteErrors{boom, nil, nil, nil}, []string{"2", "4"}},
		{"per-message: all failed", kafka.WriteErrors{boom, boom, boom, boom}, []string{}},
		{"length mismatch is not trusted", kafka.WriteErrors{nil}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deliveredIDs(batch, tc.err))
		})
	}
}
