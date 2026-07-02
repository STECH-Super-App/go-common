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
		{eventsv1.Topic_TOPIC_USER_EVENTS, "user.events"},
		{eventsv1.Topic_TOPIC_TEAM_EVENTS, "team.events"},
		{eventsv1.Topic_TOPIC_TENANT_EVENTS, "tenant.events"},
		{eventsv1.Topic_TOPIC_MEDIA_EVENTS, "media.events"},
		{eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS, "notification.events"},
		{eventsv1.Topic_TOPIC_SALE_EVENTS, "sale.events"},
		{eventsv1.Topic_TOPIC_MACHINERY_EVENTS, "machinery.events"},
		{eventsv1.Topic_TOPIC_MACHINERY_CATALOG_EVENTS, "machinery.catalog.events"},
		{eventsv1.Topic_TOPIC_MACHINERY_SERVICE_STATUS_EVENTS, "machinery.service-status.events"},
		{eventsv1.Topic_TOPIC_MACHINERY_OPERATOR_EVENTS, "machinery.operator.events"},
		{eventsv1.Topic_TOPIC_CHAT_EVENTS, "chat.events"},
		{eventsv1.Topic_TOPIC_GEO_REGION_EVENTS, "geo.region.events"},
		{eventsv1.Topic_TOPIC_RENT_EVENTS, "rent.events"},
		{eventsv1.Topic_TOPIC_ORDER_EVENTS, "order.events"},
	}
	for _, c := range cases {
		if got := events.TopicName(c.in); got != c.want {
			t.Errorf("TopicName(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTopicName_unregistered_panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("TopicName(TOPIC_UNSPECIFIED) did not panic")
		}
	}()
	events.TopicName(eventsv1.Topic_TOPIC_UNSPECIFIED)
}

// Registry completeness: every concrete enum value has exactly one wire name,
// and every wire name is grammar-valid.
func TestRegistry_coversEveryEnumValue(t *testing.T) {
	for num, name := range eventsv1.Topic_name {
		if num == int32(eventsv1.Topic_TOPIC_UNSPECIFIED) {
			continue
		}
		topic := eventsv1.Topic(num)
		wire := events.TopicName(topic) // panics if missing -> test fails
		if !events.ValidTopicName(wire) {
			t.Errorf("registry wire name %q for %s violates grammar", wire, name)
		}
	}
}

func TestDLQName(t *testing.T) {
	got := events.DLQName(eventsv1.Topic_TOPIC_USER_EVENTS, "auth")
	if want := "user.events.dlq.auth"; got != want {
		t.Errorf("DLQName = %q, want %q", got, want)
	}
	got = events.DLQName(eventsv1.Topic_TOPIC_MACHINERY_SERVICE_STATUS_EVENTS, "sale")
	if want := "machinery.service-status.events.dlq.sale"; got != want {
		t.Errorf("DLQName = %q, want %q", got, want)
	}
	got = events.DLQName(eventsv1.Topic_TOPIC_ORDER_EVENTS, "notification")
	if want := "order.events.dlq.notification"; got != want {
		t.Errorf("DLQName = %q, want %q", got, want)
	}
}

func TestRetryName(t *testing.T) {
	got := events.RetryName(eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS, "notification")
	if want := "notification.events.retry.notification"; got != want {
		t.Errorf("RetryName = %q, want %q", got, want)
	}
}

func TestDLQName_badGroup_panics(t *testing.T) {
	for _, bad := range []string{"", "Auth", "auth_svc", "-auth"} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("DLQName with group %q did not panic", bad)
				}
			}()
			events.DLQName(eventsv1.Topic_TOPIC_USER_EVENTS, bad)
		}()
	}
}

func TestValidTopicName(t *testing.T) {
	valid := []string{
		"user.events", "geo.region.events", "machinery.service-status.events",
		"user.events.dlq.auth", "notification.events.retry.notification",
	}
	for _, s := range valid {
		if !events.ValidTopicName(s) {
			t.Errorf("ValidTopicName(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"user-events",          // hyphen base, no .events tier
		"User.events",          // uppercase
		"user_events",          // underscore
		"user.events.dlq",      // dlq tier without group
		"user.events.foo.auth", // unknown tier
		"geo_region.events",    // underscore
		"",                     // empty
	}
	for _, s := range invalid {
		if events.ValidTopicName(s) {
			t.Errorf("ValidTopicName(%q) = true, want false", s)
		}
	}
}
