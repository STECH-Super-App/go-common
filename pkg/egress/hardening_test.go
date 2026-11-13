package egress_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/STECH-Super-App/go-common/pkg/egress"
)

// SL-32: NewTransport cloned http.DefaultTransport through a type assertion,
// which panics at start-up when any imported package (an OpenTelemetry HTTP
// instrumentation, a test double) has replaced the default transport.
type wrappedTransport struct{ http.RoundTripper }

func TestNewTransport_SurvivesAReplacedDefaultTransport(t *testing.T) {
	prev := http.DefaultTransport
	http.DefaultTransport = wrappedTransport{prev}
	t.Cleanup(func() { http.DefaultTransport = prev })

	var tr *http.Transport
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewTransport panicked: %v", r)
			}
		}()
		tr, err = egress.NewTransport(egress.Config{ProxyURL: "http://p:3101"})
	}()
	if err != nil || tr == nil {
		t.Fatalf("NewTransport = (%v, %v)", tr, err)
	}
	if !tr.ForceAttemptHTTP2 || tr.Proxy == nil {
		t.Fatalf("transport lost its settings: %+v", tr)
	}
	if tr.TLSHandshakeTimeout <= 0 || tr.IdleConnTimeout <= 0 || tr.DialContext == nil {
		t.Fatalf("a literal transport must carry sane timeouts: %+v", tr)
	}
}

// SL-32: HTTPClient(0) built a client with no timeout.
func TestHTTPClient_RefusesANonPositiveTimeout(t *testing.T) {
	t.Setenv("EGRESS_PROXY_URL", "http://p:3101")
	t.Setenv("EGRESS_DIRECT", "false")
	t.Setenv("APP_ENV", "dev")

	for _, d := range []time.Duration{0, -time.Second} {
		_, err := egress.HTTPClient(d)
		if err == nil {
			t.Fatalf("HTTPClient(%v) must be refused", d)
		}
		if got := reasonOf(t, err); got != egress.ReasonTimeoutInvalid {
			t.Fatalf("reason %q", got)
		}
	}
}

// SL-32: every HTTPClient call built a new transport — a new connection pool.
func TestHTTPClient_SharesOneTransportPerConfig(t *testing.T) {
	t.Setenv("EGRESS_PROXY_URL", "http://p:3101")
	t.Setenv("EGRESS_DIRECT", "false")
	t.Setenv("APP_ENV", "dev")

	a, err := egress.HTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b, err := egress.HTTPClient(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.Transport != b.Transport {
		t.Fatal("two clients on the same config must share one transport and its pool")
	}
	if a.Timeout == b.Timeout {
		t.Fatal("the timeout stays per client")
	}

	t.Setenv("EGRESS_PROXY_URL", "http://other:3102")
	c, err := egress.HTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport == a.Transport {
		t.Fatal("a different config must not reuse the transport")
	}
}
