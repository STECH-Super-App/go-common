// Package kafkaauth centralizes Kafka client authentication so every STECH
// service connects the same way to either a local plaintext broker (compose)
// or Yandex Managed Kafka (SASL/SCRAM-SHA-512 over TLS).
//
// Backward compatible: when KAFKA_USER is empty (local dev), Transport returns
// kafka.DefaultTransport and Dialer returns kafka.DefaultDialer — i.e. the
// previous plaintext behaviour. When KAFKA_USER is set (cluster), both return
// SASL+TLS configs.
//
// TLS uses InsecureSkipVerify because the Yandex Managed Kafka CA is not
// bundled in service images; the wire is still encrypted (parity with the
// Postgres sslmode=require choice). Bundle the YC CA + flip this off as a
// later hardening step.
package kafkaauth

import (
	"crypto/tls"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
)

func credentials() (string, string) {
	return os.Getenv("KAFKA_USER"), os.Getenv("KAFKA_PASSWORD")
}

func tlsConfig() *tls.Config {
	// G402: intentional — YC Managed Kafka CA is not bundled; TLS still encrypts.
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec
}

// Transport returns a kafka.RoundTripper for SASL/SCRAM-SHA-512 over TLS when
// KAFKA_USER is set, else kafka.DefaultTransport. It must hand back the library
// default (never a nil *kafka.Transport) because kafka.Writer.Transport is an
// interface field: a typed-nil pointer stored in an interface is itself non-nil,
// so kafka-go would skip its DefaultTransport fallback and dereference the nil
// transport on the first write. Assign unconditionally:
//
//	w := &kafka.Writer{Addr: kafka.TCP(brokers...), Transport: kafkaauth.Transport()}
func Transport() kafka.RoundTripper {
	user, pass := credentials()
	if user == "" {
		return kafka.DefaultTransport
	}
	mech, err := scram.Mechanism(scram.SHA512, user, pass)
	if err != nil {
		panic("kafkaauth: invalid SCRAM credentials: " + err.Error())
	}
	return &kafka.Transport{
		SASL: mech,
		TLS:  tlsConfig(),
	}
}

// Dialer returns a *kafka.Dialer for SASL/SCRAM-SHA-512 over TLS when
// KAFKA_USER is set, else kafka.DefaultDialer. Use in ReaderConfig.Dialer.
func Dialer() *kafka.Dialer {
	user, pass := credentials()
	if user == "" {
		return kafka.DefaultDialer
	}
	mech, err := scram.Mechanism(scram.SHA512, user, pass)
	if err != nil {
		panic("kafkaauth: invalid SCRAM credentials: " + err.Error())
	}
	return &kafka.Dialer{
		Timeout:       10 * time.Second,
		DualStack:     true,
		SASLMechanism: mech,
		TLS:           tlsConfig(),
	}
}
