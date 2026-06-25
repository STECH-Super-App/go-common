package events

import (
	"fmt"
	"regexp"
	"strings"

	eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
)

// wireNames is the canonical topic registry: every base Topic enum value maps
// to its exact Kafka wire name under the dotted hierarchical grammar
// (<domain>[.<subdomain>].events). A multi-word segment keeps an inner hyphen
// (service-status). This is the single source of truth — do NOT derive names
// mechanically from the enum identifier, because compounds must not split into
// separate dot segments.
var wireNames = map[eventsv1.Topic]string{
	eventsv1.Topic_TOPIC_USER_EVENTS:                     "user.events",
	eventsv1.Topic_TOPIC_TEAM_EVENTS:                     "team.events",
	eventsv1.Topic_TOPIC_TENANT_EVENTS:                   "tenant.events",
	eventsv1.Topic_TOPIC_MEDIA_EVENTS:                    "media.events",
	eventsv1.Topic_TOPIC_NOTIFICATION_EVENTS:             "notification.events",
	eventsv1.Topic_TOPIC_SALE_EVENTS:                     "sale.events",
	eventsv1.Topic_TOPIC_MACHINERY_EVENTS:                "machinery.events",
	eventsv1.Topic_TOPIC_MACHINERY_CATALOG_EVENTS:        "machinery.catalog.events",
	eventsv1.Topic_TOPIC_MACHINERY_SERVICE_STATUS_EVENTS: "machinery.service-status.events",
	eventsv1.Topic_TOPIC_MACHINERY_OPERATOR_EVENTS:       "machinery.operator.events",
	eventsv1.Topic_TOPIC_CHAT_EVENTS:                     "chat.events",
	eventsv1.Topic_TOPIC_GEO_REGION_EVENTS:               "geo.region.events",
	eventsv1.Topic_TOPIC_RENT_EVENTS:                     "rent.events",
}

// TopicName returns the canonical wire name for a base topic.
// Panics on an unregistered/unspecified topic — callers must pass a registered topic.
func TopicName(t eventsv1.Topic) string {
	name, ok := wireNames[t]
	if !ok {
		panic(fmt.Sprintf("events.TopicName: %s is not a registered topic", t))
	}
	return name
}

// DLQName returns the dead-letter topic for a (base topic, consumer group)
// pair: "<wire>.dlq.<group>". group must be a lowercase grammar-valid segment
// (the consuming service's short name, e.g. "auth", "inbox").
func DLQName(t eventsv1.Topic, group string) string {
	mustSegment(group)
	return TopicName(t) + ".dlq." + group
}

// RetryName returns the retry topic for a (base topic, consumer group) pair:
// "<wire>.retry.<group>".
func RetryName(t eventsv1.Topic, group string) string {
	mustSegment(group)
	return TopicName(t) + ".retry." + group
}

var segmentRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func mustSegment(s string) {
	if !segmentRe.MatchString(s) {
		panic(fmt.Sprintf("events: %q is not a valid lowercase topic segment (want %s)", s, segmentRe.String()))
	}
}

// topicRe matches the full grammar: a domain segment plus zero or more
// subdomain segments, ending in ".events", optionally followed by a
// ".dlq.<group>" or ".retry.<group>" tier.
var topicRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)*\.events(\.(dlq|retry)\.[a-z0-9]+(-[a-z0-9]+)*)?$`)

// ValidTopicName reports whether s conforms to the STECH topic grammar.
// Rejects underscores outright (Kafka metric collision with dots).
func ValidTopicName(s string) bool {
	if strings.Contains(s, "_") {
		return false
	}
	return topicRe.MatchString(s)
}
