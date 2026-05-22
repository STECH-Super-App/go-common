package kafkaauth

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

// Regression: with no KAFKA_USER (local/plaintext dev), Transport must return a
// usable non-nil RoundTripper (kafka.DefaultTransport), never a typed-nil
// *kafka.Transport. A typed-nil pointer stored in kafka.Writer.Transport (an
// interface) is itself non-nil, so kafka-go skips its DefaultTransport fallback
// and panics with a nil-pointer dereference on the first write.
func TestTransportWithoutCredentialsReturnsDefault(t *testing.T) {
	t.Setenv("KAFKA_USER", "")
	t.Setenv("KAFKA_PASSWORD", "")

	rt := Transport()
	if rt == nil {
		t.Fatal("Transport() returned a nil interface; want kafka.DefaultTransport")
	}
	if rt != kafka.DefaultTransport {
		t.Errorf("want kafka.DefaultTransport, got %#v", rt)
	}

	// Storing it in the Writer's interface field must not leave a typed-nil.
	w := kafka.Writer{Transport: rt}
	if w.Transport == nil {
		t.Fatal("Writer.Transport is nil after assignment; typed-nil leaked")
	}
}

// With credentials set, Transport returns a SASL/SCRAM-SHA-512 + TLS transport.
func TestTransportWithCredentialsReturnsSASLTransport(t *testing.T) {
	t.Setenv("KAFKA_USER", "svc")
	t.Setenv("KAFKA_PASSWORD", "secret")

	tr, ok := Transport().(*kafka.Transport)
	if !ok {
		t.Fatalf("want *kafka.Transport, got %T", Transport())
	}
	if tr.SASL == nil {
		t.Error("SASL mechanism not set on transport")
	}
	if tr.TLS == nil {
		t.Error("TLS config not set on transport")
	}
}
