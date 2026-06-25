package events

import (
	"fmt"
	"regexp"
	"strings"

	eventsv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/v1"
	"google.golang.org/protobuf/proto"
)

// wireNames is the canonical topic registry, derived ONCE at init from the
// proto wire_name option on each events.v1.Topic value — the single,
// cross-language source of truth (proto-contracts proto/events/v1/topics.proto).
// go-common no longer hand-maintains topic names: adding a Topic enum value with
// its wire_name is all that is required; this map and every caller of TopicName
// pick it up automatically once gen-go-lib regenerates. Names are NOT derived
// from the identifier (a compound like service-status must not split into dot
// segments) — they are read verbatim from the contract.
var wireNames = buildWireNames()

// buildWireNames reads the wire_name option off every concrete Topic value. A
// concrete value with no annotation is a contract-authoring error and panics at
// startup (a loud early accident, caught by every test and in every env) rather
// than silently routing to "".
func buildWireNames() map[eventsv1.Topic]string {
	m := make(map[eventsv1.Topic]string)
	vals := eventsv1.Topic(0).Descriptor().Values()
	for i := 0; i < vals.Len(); i++ {
		ev := vals.Get(i)
		topic := eventsv1.Topic(ev.Number())
		if topic == eventsv1.Topic_TOPIC_UNSPECIFIED {
			continue
		}
		name, _ := proto.GetExtension(ev.Options(), eventsv1.E_WireName).(string)
		if name == "" {
			panic(fmt.Sprintf("events: Topic %s has no wire_name option", ev.Name()))
		}
		m[topic] = name
	}
	return m
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
