package egress

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

const (
	// ReasonNotConfigured marks: no proxy URL and no permitted local direct opt-out.
	ReasonNotConfigured = "COMMON_EGRESS_NOT_CONFIGURED"
	// ReasonProxyURLInvalid marks: the proxy URL is not a bare http://host:port.
	ReasonProxyURLInvalid = "COMMON_EGRESS_PROXY_URL_INVALID"
	// ReasonTimeoutInvalid marks: HTTPClient was asked for a zero or negative
	// timeout, which would mean a client that waits forever.
	ReasonTimeoutInvalid = "COMMON_EGRESS_TIMEOUT_INVALID"

	envProxyURL = "EGRESS_PROXY_URL"
	envDirect   = "EGRESS_DIRECT"
	envAppEnv   = "APP_ENV"
	appEnvLocal = "local"
)

// Config selects how outbound requests leave the process.
type Config struct {
	ProxyURL string // http://host:port of the egress proxy listener
	Direct   bool   // skip the proxy; honoured only when AppEnv == "local"
	AppEnv   string
}

// ConfigFromEnv reads EGRESS_PROXY_URL, EGRESS_DIRECT ("true" only) and APP_ENV.
func ConfigFromEnv() Config {
	return Config{
		ProxyURL: os.Getenv(envProxyURL),
		Direct:   os.Getenv(envDirect) == "true",
		AppEnv:   os.Getenv(envAppEnv),
	}
}

// newBaseTransport builds the transport from a literal with the values
// net/http's DefaultTransport carries. Cloning http.DefaultTransport instead
// needs a type assertion that panics at start-up as soon as any imported
// package has wrapped the default transport (SL-32).
func newBaseTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewTransport returns a new transport (the settings of http.DefaultTransport) whose Proxy is the
// configured egress proxy (or nil for the local direct opt-out). It never
// consults the environment's proxy variables.
func NewTransport(cfg Config) (*http.Transport, error) {
	tr := newBaseTransport()

	if cfg.ProxyURL == "" {
		if cfg.Direct && cfg.AppEnv == appEnvLocal {
			tr.Proxy = nil
			return tr, nil
		}
		return nil, commonErrors.New(http.StatusInternalServerError).
			Reason(ReasonNotConfigured).
			Message("egress proxy is not configured: set EGRESS_PROXY_URL").
			Build()
	}

	u, err := parseProxyURL(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	tr.Proxy = http.ProxyURL(u)
	return tr, nil
}

// Transport is NewTransport(ConfigFromEnv()).
func Transport() (*http.Transport, error) {
	return NewTransport(ConfigFromEnv())
}

// HTTPClient builds a client with the given total timeout on the transport of
// ConfigFromEnv(). The timeout must be positive: a zero http.Client.Timeout
// waits forever, so it is refused with COMMON_EGRESS_TIMEOUT_INVALID.
//
// Clients built from the same configuration share ONE transport, and so one
// connection pool; NewTransport and Transport still return a fresh transport
// each call, which the caller owns and may adjust.
func HTTPClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		return nil, commonErrors.New(http.StatusInternalServerError).
			Reason(ReasonTimeoutInvalid).
			Message("egress HTTPClient needs a positive timeout").
			Build()
	}
	tr, err := sharedTransport(ConfigFromEnv())
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

var (
	sharedMu         sync.Mutex
	sharedTransports = map[Config]*http.Transport{}
)

// sharedTransport returns the one transport HTTPClient uses for cfg.
func sharedTransport(cfg Config) (*http.Transport, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if tr, ok := sharedTransports[cfg]; ok {
		return tr, nil
	}
	tr, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	sharedTransports[cfg] = tr
	return tr, nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	invalid := func(cause error) error {
		b := commonErrors.New(http.StatusInternalServerError).
			Reason(ReasonProxyURLInvalid).
			Message("EGRESS_PROXY_URL must be http://host:port with no userinfo, path or query")
		if cause != nil {
			b = b.Cause(cause)
		}
		return b.Build()
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, invalid(err)
	}
	if u.Scheme != "http" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, invalid(nil)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return nil, invalid(err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, invalid(nil)
	}
	return u, nil
}
