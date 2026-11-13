package kafkawriter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/kafkawriter"
)

func TestNewProducer_SafeSettings(t *testing.T) {
	w := kafkawriter.NewProducer([]string{"a:9092", "b:9092"})

	assert.Equal(t, kafka.RequireAll, w.RequiredAcks)
	assert.False(t, w.Async)
	assert.False(t, w.AllowAutoTopicCreation)
	assert.Equal(t, 5, w.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, w.WriteBackoffMin)
	assert.Equal(t, time.Second, w.WriteBackoffMax)
	assert.Empty(t, w.Topic, "an outbox writer carries no fixed topic: every row names its own")
	assert.IsType(t, &kafka.Hash{}, w.Balancer, "a domain event's key must keep its partition")
	assert.NotNil(t, w.Transport, "kafka.Writer.Transport is an interface: nil would skip kafka-go's default only by accident")
	require.NoError(t, kafkawriter.Check(w))
}

func TestNewTopicWriter_FixedTopicSameSafety(t *testing.T) {
	w := kafkawriter.NewTopicWriter([]string{"a:9092"}, "user.events.dlq.order")

	assert.Equal(t, "user.events.dlq.order", w.Topic)
	assert.Equal(t, kafka.RequireAll, w.RequiredAcks)
	assert.False(t, w.Async)
	require.NoError(t, kafkawriter.Check(w))
}

func TestOptions(t *testing.T) {
	w := kafkawriter.NewProducer([]string{"a:9092"}, kafkawriter.WithCompression(kafka.Snappy))
	assert.Equal(t, kafka.Snappy, w.Compression)
	// The safety settings cannot be reached through an option.
	assert.Equal(t, kafka.RequireAll, w.RequiredAcks)
}

func TestCheck(t *testing.T) {
	cases := map[string]struct {
		w    *kafka.Writer
		okay bool
	}{
		"nil":               {nil, false},
		"zero value":        {&kafka.Writer{}, false},
		"RequireNone":       {&kafka.Writer{RequiredAcks: kafka.RequireNone}, false},
		"async RequireAll":  {&kafka.Writer{RequiredAcks: kafka.RequireAll, Async: true}, false},
		"async RequireOne":  {&kafka.Writer{RequiredAcks: kafka.RequireOne, Async: true}, false},
		"RequireOne":        {&kafka.Writer{RequiredAcks: kafka.RequireOne}, true},
		"RequireAll":        {&kafka.Writer{RequiredAcks: kafka.RequireAll}, true},
		"auto topic create": {&kafka.Writer{RequiredAcks: kafka.RequireAll, AllowAutoTopicCreation: true}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := kafkawriter.Check(tc.w)
			if tc.okay {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var ae *apperr.AppError
			require.True(t, errors.As(err, &ae), "Rule 7: the refusal is a typed error")
			assert.Equal(t, kafkawriter.ReasonUnsafeWriter, ae.Reason)
			assert.Equal(t, 500, ae.Code)
		})
	}
}

func TestMustCheck_PanicsWithTypedErrorNamingTheCaller(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r)
		ae, ok := r.(*apperr.AppError)
		require.True(t, ok, "panic value must be the typed error, got %T", r)
		assert.Equal(t, kafkawriter.ReasonUnsafeWriter, ae.Reason)
		assert.Contains(t, ae.Message, "my.caller")
	}()
	kafkawriter.MustCheck("my.caller", &kafka.Writer{})
}
