package events

import (
	"fmt"
	"strings"

	eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
)

// TopicName converts a Topic enum value into its wire name.
// Convention: strip "TOPIC_" prefix, lowercase, underscore->hyphen.
// E.g. TOPIC_USER_EVENTS -> "user-events".
//
// Panics on TOPIC_UNSPECIFIED — callers must pass a concrete topic.
func TopicName(t eventsv1.Topic) string {
	if t == eventsv1.Topic_TOPIC_UNSPECIFIED {
		panic(fmt.Sprintf("events.TopicName: %s is not a valid topic", t))
	}
	name := strings.TrimPrefix(t.String(), "TOPIC_")
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}
