package egress_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/STECH-Super-App/go-common/pkg/egress"
	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	var app *commonErrors.AppError
	if !errors.As(err, &app) {
		t.Fatalf("want *AppError, got %T (%v)", err, err)
	}
	return app.Reason
}

// connectProxy is a bare TCP listener that records the CONNECT authority.
func connectProxy(t *testing.T) (addr string, authorities func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				mu.Lock()
				got = append(got, req.Host)
				mu.Unlock()
				_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			}(c)
		}
	}()
	return ln.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func doGet(t *testing.T, tr *http.Transport, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func TestNewTransportRoutesThroughProxy(t *testing.T) {
	addr, authorities := connectProxy(t)
	tr, err := egress.NewTransport(egress.Config{ProxyURL: "http://" + addr, AppEnv: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 must be true")
	}
	doGet(t, tr, "https://example.test/x")
	got := authorities()
	if len(got) != 1 || got[0] != "example.test:443" {
		t.Fatalf("CONNECT authorities: %v", got)
	}
}

func TestNewTransportIgnoresEnvProxy(t *testing.T) {
	envAddr, envSeen := connectProxy(t)
	t.Setenv("HTTPS_PROXY", "http://"+envAddr)
	t.Setenv("HTTP_PROXY", "http://"+envAddr)

	addr, authorities := connectProxy(t)
	tr, err := egress.NewTransport(egress.Config{ProxyURL: "http://" + addr})
	if err != nil {
		t.Fatal(err)
	}
	doGet(t, tr, "https://example.test/x")
	if len(authorities()) != 1 {
		t.Fatalf("configured proxy not used: %v", authorities())
	}
	if len(envSeen()) != 0 {
		t.Fatalf("env proxy was used: %v", envSeen())
	}

	direct, err := egress.NewTransport(egress.Config{Direct: true, AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if direct.Proxy != nil {
		t.Fatal("Direct transport must have a nil Proxy")
	}
}

func TestEmptyURLIsNotConfigured(t *testing.T) {
	for _, cfg := range []egress.Config{
		{AppEnv: "dev"},
		{AppEnv: ""},
		{AppEnv: "local"},
	} {
		_, err := egress.NewTransport(cfg)
		if got := reasonOf(t, err); got != egress.ReasonNotConfigured {
			t.Fatalf("%+v: reason %q", cfg, got)
		}
	}
}

func TestDirectOnlyInLocal(t *testing.T) {
	_, err := egress.NewTransport(egress.Config{Direct: true, AppEnv: "dev"})
	if got := reasonOf(t, err); got != egress.ReasonNotConfigured {
		t.Fatalf("reason %q", got)
	}
}

func TestInvalidProxyURL(t *testing.T) {
	for _, u := range []string{"https://x:1", "http://x", "http://u:p@x:1", "http://x:1/path", "http://x:1?q=1", "://bad", "http://x:abc", "http://:1"} {
		_, err := egress.NewTransport(egress.Config{ProxyURL: u})
		if got := reasonOf(t, err); got != egress.ReasonProxyURLInvalid {
			t.Fatalf("%q: reason %q", u, got)
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("EGRESS_PROXY_URL", "http://p:3101")
	t.Setenv("EGRESS_DIRECT", "true")
	t.Setenv("APP_ENV", "local")
	want := egress.Config{ProxyURL: "http://p:3101", Direct: true, AppEnv: "local"}
	if got := egress.ConfigFromEnv(); got != want {
		t.Fatalf("got %+v", got)
	}
	t.Setenv("EGRESS_DIRECT", "TRUE")
	if egress.ConfigFromEnv().Direct {
		t.Fatal(`only "true" enables Direct`)
	}
}

func TestTransportFromEnv(t *testing.T) {
	t.Setenv("EGRESS_PROXY_URL", "")
	t.Setenv("EGRESS_DIRECT", "false")
	t.Setenv("APP_ENV", "dev")
	_, err := egress.Transport()
	if got := reasonOf(t, err); got != egress.ReasonNotConfigured {
		t.Fatalf("reason %q", got)
	}
}

func TestHTTPClientTimeout(t *testing.T) {
	t.Setenv("EGRESS_PROXY_URL", "http://p:3101")
	t.Setenv("EGRESS_DIRECT", "false")
	t.Setenv("APP_ENV", "dev")
	c, err := egress.HTTPClient(7 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 7*time.Second || c.Transport == nil {
		t.Fatalf("client: %+v", c)
	}
	t.Setenv("EGRESS_PROXY_URL", "")
	if _, err := egress.HTTPClient(time.Second); err == nil {
		t.Fatal("want error")
	}
}
