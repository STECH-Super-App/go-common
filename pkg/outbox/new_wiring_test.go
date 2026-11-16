package outbox

// What outbox.New wires together, tested THROUGH New: a sampler test built by
// hand proved the by-topic gauges work while New never asked for them (R2).

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/kafkawriter"
)

func TestNew_ExportsTheByTopicBacklogGauges(t *testing.T) {
	pool, store := migratedDB(t)
	base := time.Now().UTC().Add(-90 * time.Second)
	for i := 0; i < 3; i++ {
		insertTopicMessage(t, pool, store, newID(), "wired.stuck.events", "k", base.Add(time.Duration(i)*time.Second))
	}
	insertTopicMessage(t, pool, store, newID(), "wired.other.events", "k", time.Now().UTC())

	o := New(pool, kafkawriter.NewProducer([]string{"127.0.0.1:1"}), zap.NewNop(), DefaultConfig(), "wired.default.events")
	o.sampler.collect(context.Background())

	assert.Equal(t, 3.0, topicGaugeValue(t, "outbox_topic_pending_messages", "wired.stuck.events"))
	assert.Equal(t, 1.0, topicGaugeValue(t, "outbox_topic_pending_messages", "wired.other.events"))
	assert.Greater(t, topicGaugeValue(t, "outbox_topic_oldest_pending_age_seconds", "wired.stuck.events"), 80.0,
		"the age gauge names the topic whose oldest row is old")
	assert.Less(t, topicGaugeValue(t, "outbox_topic_oldest_pending_age_seconds", "wired.other.events"), 30.0)
	require.NotZero(t, gaugeValue(t, "outbox_pending_messages"), "the unlabelled totals are still published")
}
