// Package violations holds one violation of every guard: the forbidigo and
// depguard rules of .golangci.strict.yml (scripts/check-guards.sh lints it and
// fails when a rule stops firing) and the AST rules of pkg/lintguards (its test
// scans it). It lives under testdata so `go build ./...` and `go test ./...`
// skip it.
package violations

import (
	"context"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/clientip"
	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

func kafkaWriterLiteral() *kafka.Writer {
	return &kafka.Writer{Addr: kafka.TCP("x:9092")} // lintguards: kafka.Writer literal
}

func kafkaNewWriter() *kafka.Writer {
	return kafka.NewWriter(kafka.WriterConfig{}) // want forbidigo: kafka.NewWriter
}

func bareEcho() *echo.Echo {
	return echo.New() // want forbidigo: echo.New
}

func notify() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt) // want forbidigo: signal.Notify
}

func stdlibLog() {
	log.Printf("hello") // want depguard: log
}

func headerLiteral(r *http.Request) string {
	return r.Header.Get("X-Forwarded-For") // lintguards: client address header
}

func clientIP(c echo.Context) string {
	return c.RealIP() // want forbidigo: RealIP
}

func xffConstant() string {
	return echo.HeaderXForwardedFor // want forbidigo: HeaderXForwardedFor
}

func peerAddress(r *http.Request) string {
	return r.RemoteAddr // want forbidigo: RemoteAddr
}

func authHeaderConstant() string {
	return auth.HeaderRealIP // want forbidigo: auth.HeaderRealIP
}

func assertClientAddress(h http.Header) {
	clientip.Set(h, netip.Addr{}) // want forbidigo: clientip.Set
}

func httpClientLiteral() *http.Client {
	return &http.Client{} // lintguards: http.Client literal
}

func httpDefaultClient() *http.Client {
	return http.DefaultClient // want forbidigo: http.DefaultClient
}

func dial() (*grpc.ClientConn, error) {
	return grpc.NewClient("x:1") // want forbidigo: grpc.NewClient
}

func cjson(c echo.Context) error {
	return c.JSON(200, nil)
}

// --- release 2 guards (outbox, consumer, idempotency) ---

func idempotencyHeader(r *http.Request) string {
	return r.Header.Get("Idempotency-Key") // lintguards: Idempotency-Key header literal
}

func idempotencyTable() string {
	return "INSERT INTO idempotency_records (key) VALUES ($1)" // lintguards: hand-rolled idempotency table
}

func libraryIdempotencyTable() string {
	return "SELECT resource_id FROM common_idempotency_keys WHERE key = $1" // lintguards: hand-rolled idempotency table (the library's own table is not a service's to read)
}

func idempotencyHeaderValues(r *http.Request) []string {
	return r.Header.Values("idempotency-key") // lintguards: Idempotency-Key header literal (any case, any accessor)
}

func dedupTable() string {
	return "INSERT INTO processed_events (event_id) VALUES ($1)" // lintguards: hand-rolled deduplicator
}

func outboxSQL() string {
	return "SELECT id FROM outbox_messages WHERE status = 'pending'" // lintguards: hand-rolled outbox access
}

func memoryDedup() events.Deduplicator {
	return events.NewMemoryDeduplicator() // want forbidigo: NewMemoryDeduplicator; lintguards: in-memory deduplicator
}

func oldDedup(pool *pgxpool.Pool) *outbox.Deduplicator {
	return outbox.NewDeduplicator(pool) // want forbidigo: outbox.NewDeduplicator; lintguards (opt-in): pool in a handler package
}

func readerLoop(ctx context.Context, r *kafka.Reader) {
	for {
		m, err := r.FetchMessage(ctx) // want forbidigo: kafka.Reader.FetchMessage
		if err != nil {
			return
		}
		_ = r.CommitMessages(ctx, m) // want forbidigo: kafka.Reader.CommitMessages
	}
}

func legacyHandler(d *events.Dispatcher) {
	events.Handle(d, func(context.Context, *usersv1.UserRegistered) error { return nil }) // lintguards (opt-in): legacy event handler form
}

// --- lookalikes: NOT reported (R12). Everything below is legitimate; the
// lintguards test fails when any rule flags a line from here down. ---

func lookalikes() []string {
	return []string{
		"SELECT id FROM orders WHERE idempotency_key = $1",                      // a column, not a table
		"INSERT INTO conversation_processed_events(event_id) VALUES($1)",        // a longer table name
		"SELECT 1 FROM conversation_messages WHERE client_idempotency_key = $1", // a column
		"Content-Type,Authorization,Idempotency-Key,Range",                      // a CORS / config default
		"Idempotency-Key", // an allow-list entry or field name, not a read
	}
}
