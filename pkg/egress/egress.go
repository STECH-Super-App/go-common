package egress

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

const (
	// ReasonNotConfigured marks: no proxy URL and no permitted local direct opt-out.
	ReasonNotConfigured = "COMMON_EGRESS_NOT_CONFIGURED"
	// ReasonProxyURLInvalid marks: the proxy URL is not a bare http://host:port.
	ReasonProxyURLInvalid = "COMMON_EGRESS_PROXY_URL_INVALID"

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

// NewTransport returns a clone of http.DefaultTransport whose Proxy is the
// configured egress proxy (or nil for the local direct opt-out). It never
// consults the environment's proxy variables.
func NewTransport(cfg Config) (*http.Transport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true

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

// HTTPClient builds a client on Transport() with the given total timeout.
func HTTPClient(timeout time.Duration) (*http.Client, error) {
	tr, err := Transport()
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
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
