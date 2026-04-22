package events_test

import (
	"testing"

	eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

func TestTopicName(t *testing.T) {
	cases := []struct {
		in   eventsv1.Topic
		want string
	}{
		{eventsv1.Topic_TOPIC_USER_EVENTS, "user-events"},
		{eventsv1.Topic_TOPIC_TEAM_EVENTS, "team-events"},
		{eventsv1.Topic_TOPIC_TENANT_EVENTS, "tenant-events"},
		{eventsv1.Topic_TOPIC_MEDIA_EVENTS, "media-events"},
		{eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS, "notification-events"},
		{eventsv1.Topic_TOPIC_USER_EVENTS_DLQ, "user-events-dlq"},
		{eventsv1.Topic_TOPIC_TEAM_EVENTS_DLQ, "team-events-dlq"},
		{eventsv1.Topic_TOPIC_TENANT_EVENTS_DLQ, "tenant-events-dlq"},
		{eventsv1.Topic_TOPIC_MEDIA_EVENTS_DLQ, "media-events-dlq"},
		{eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS_DLQ, "notification-events-dlq"},
		{eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS_RETRY, "notification-events-retry"},
	}
	for _, c := range cases {
		got := events.TopicName(c.in)
		if got != c.want {
			t.Errorf("TopicName(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTopicName_unspecified_panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("TopicName(TOPIC_UNSPECIFIED) did not panic")
		}
	}()
	events.TopicName(eventsv1.Topic_TOPIC_UNSPECIFIED)
}
