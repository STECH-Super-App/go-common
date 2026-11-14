// Package kafkawriter is the one place a STECH service gets a Kafka producer
// from. It builds the writers behind the outbox relay and behind the
// dispatcher's DLQ and retry tiers with the settings Critical Rules 5 and 13
// require, and it checks a writer a caller built by hand.
//
// Why a constructor and a check, not a README line: kafka-go's zero value is
// RequiredAcks = RequireNone. Under it WriteMessages returns nil before any
// broker answered, so the relay marks the outbox row sent and the dispatcher
// commits the source offset behind a "successful" DLQ write — and the event is
// gone without a single error anywhere (audit finding SL-01). Async does the
// same by design: it returns before the write happens. Seven services set
// RequireAll by hand and seven did not.
//
//	w := kafkawriter.NewProducer(cfg.KafkaBrokers)               // outbox relay
//	dlq := kafkawriter.NewTopicWriter(cfg.KafkaBrokers, dlqTopic) // DLQ / retry
//
// outbox.New and outbox.NewRelay call CheckProducer (Check plus a key-hashing
// balancer); events.NewDispatcher calls Check on its DLQ and WithRetry writers.
// Each panics on a writer it refuses, so a service that still builds an unsafe
// one fails at start-up instead of losing or reordering events in production.
package kafkawriter

import (
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/kafkaauth"
)

// Critical Rule 5: the retry triple every producer carries.
const (
	// MaxAttempts is how often kafka-go tries one write before it gives up.
	MaxAttempts = 5
	// BackoffFloor is the first wait between attempts (kafka.Writer.WriteBackoffMin).
	BackoffFloor = 100 * time.Millisecond
	// BackoffCeiling caps the exponential wait between attempts (kafka.Writer.WriteBackoffMax).
	BackoffCeiling = time.Second
)

// ReasonUnsafeWriter is the Reason of the error Check returns.
const ReasonUnsafeWriter = "COMMON_KAFKA_WRITER_UNSAFE"

// Option tunes a writer built by NewProducer or NewTopicWriter. The safety
// settings (acks, async, retries, auto-creation) have no option on purpose.
type Option func(*kafka.Writer)

// WithCompression sets the batch compression codec (default: none).
func WithCompression(c kafka.Compression) Option {
	return func(w *kafka.Writer) { w.Compression = c }
}

// WithTransport replaces the default kafkaauth.Transport(). Tests use it; a
// service has no reason to.
func WithTransport(rt kafka.RoundTripper) Option {
	return func(w *kafka.Writer) { w.Transport = rt }
}

// NewProducer builds the writer the outbox relay publishes through: no fixed
// Topic (every outbox row names its own), RequireAll, the Hash balancer (so a
// domain event's key keeps its partition and its order — a nil balancer is
// round-robin and LeastBytes ignores the key), the Rule 5 retry triple,
// synchronous, and no topic auto-creation (Critical Rule 13: topics are
// declared, never created by a service). Transport is kafkaauth.Transport().
func NewProducer(brokers []string, opts ...Option) *kafka.Writer {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		MaxAttempts:            MaxAttempts,
		WriteBackoffMin:        BackoffFloor,
		WriteBackoffMax:        BackoffCeiling,
		Async:                  false,
		AllowAutoTopicCreation: false,
		Transport:              kafkaauth.Transport(),
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// NewTopicWriter builds a writer fixed to one topic — the DLQ or retry tier of
// a consumer group (events.DLQName / events.RetryName). Same settings as
// NewProducer; the message key is kept, so a dead-lettered message stays on
// the partition of its key.
func NewTopicWriter(brokers []string, topic string, opts ...Option) *kafka.Writer {
	w := NewProducer(brokers, opts...)
	w.Topic = topic
	return w
}

// Check reports why w must not carry outbox or DLQ traffic, or nil when it may.
// It refuses a nil writer, an Async writer and a RequireNone writer (the zero
// value) — the three ways WriteMessages can return success for a message no
// broker has acknowledged. RequireOne is accepted: it is weaker than
// RequireAll but still waits for the partition leader.
//
// The returned error is a go-common *errors.AppError (500,
// ReasonUnsafeWriter); its Message names the offending setting.
func Check(w *kafka.Writer) error {
	switch {
	case w == nil:
		return unsafe("writer is nil")
	case w.Async:
		return unsafe("writer is Async: WriteMessages returns before the broker has the message")
	case w.RequiredAcks == kafka.RequireNone:
		return unsafe("writer has RequiredAcks=RequireNone (the zero value): WriteMessages returns before any broker answered; set kafka.RequireAll or build the writer with kafkawriter.NewProducer")
	}
	return nil
}

// CheckProducer is Check plus the one setting only a domain-event producer
// needs: the writer must partition by the message key (Critical Rule 13).
// kafka-go's nil Balancer is round-robin and LeastBytes ignores the key, so on a
// multi-partition topic the events of one aggregate would land on different
// partitions and be consumed out of order. Accepted: the key-hashing balancers
// (*kafka.Hash, *kafka.ReferenceHash, kafka.CRC32Balancer, kafka.Murmur2Balancer;
// NewProducer sets &kafka.Hash{}). The outbox relay's writer is checked with it;
// DLQ and retry writers (NewTopicWriter, events.NewDispatcher) are not — a
// dead letter has no ordering to keep, and Check is what they get.
func CheckProducer(w *kafka.Writer) error {
	if err := Check(w); err != nil {
		return err
	}
	switch w.Balancer.(type) {
	case *kafka.Hash, *kafka.ReferenceHash, kafka.CRC32Balancer, *kafka.CRC32Balancer,
		kafka.Murmur2Balancer, *kafka.Murmur2Balancer:
		return nil
	}
	return unsafe(fmt.Sprintf("writer's Balancer is %T: a domain event must stay on the partition of its key; "+
		"set Balancer: &kafka.Hash{} or build the writer with kafkawriter.NewProducer", w.Balancer))
}

// MustCheckProducer is CheckProducer for a constructor that has no error return.
func MustCheckProducer(who string, w *kafka.Writer) {
	if err := CheckProducer(w); err != nil {
		panic(errors.New(500).
			Reason(ReasonUnsafeWriter).
			Message(who + ": " + err.Error()).
			Build())
	}
}

// MustCheck is Check for a constructor that has no error return: it panics
// with the *errors.AppError. who names the caller in the message.
func MustCheck(who string, w *kafka.Writer) {
	if err := Check(w); err != nil {
		panic(errors.New(500).
			Reason(ReasonUnsafeWriter).
			Message(who + ": " + err.Error()).
			Build())
	}
}

func unsafe(why string) error {
	return errors.New(500).Reason(ReasonUnsafeWriter).Message("kafkawriter: " + why).Build()
}
