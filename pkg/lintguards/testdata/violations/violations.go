// Package violations holds one violation of every guard: the forbidigo and
// depguard rules of .golangci.strict.yml (scripts/check-guards.sh lints it and
// fails when a rule stops firing) and the AST rules of pkg/lintguards (its test
// scans it). It lives under testdata so `go build ./...` and `go test ./...`
// skip it.
package violations

import (
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/labstack/echo/v4"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
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
