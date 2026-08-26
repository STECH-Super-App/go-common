package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// DefaultBuckets is the latency bucket set shared by every STECH duration
// histogram (design spec §3.1). Dashboards and alert expressions are written
// against these exact boundaries — changing them invalidates recorded history,
// so treat the list as a contract, not a tuning knob.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// RouteUnmatched is the clamp value for requests that matched no route.
// Echo leaves Context.Path() empty when the router found nothing, so without
// this clamp every vulnerability scanner would mint a new series per probed
// URL (trap §9.3).
const RouteUnmatched = "unmatched"

// HTTPRequestDuration is the fleet-wide HTTP server histogram (§3.1).
//
//	http_request_duration_seconds{method, route, status_class}
//
// The request rate derives from its _count; there is deliberately no separate
// counter. Service identity comes from the scrape job label, never from a
// metric-name prefix.
//
// It is observed by middleware.Metrics(); services do not touch it directly.
var HTTPRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP server request duration in seconds, by method, route template and status class.",
		Buckets: DefaultBuckets,
	},
	[]string{"method", "route", "status_class"},
)

// Instruments register at package init — never inside a middleware factory or
// a server constructor, because service tests rebuild those repeatedly in one
// process and MustRegister panics on the second call (§3.0, trap §9.4).
func init() {
	Registry.MustRegister(HTTPRequestDuration)
}

// StatusClass buckets an HTTP status code into the 1xx..5xx label value used
// by the status_class label. Raw codes are deliberately never used as a label
// value (§3.6 cardinality budget).
func StatusClass(code int) string {
	class := code / 100
	if class < 1 || class > 5 {
		return "5xx"
	}
	return strconv.Itoa(class) + "xx"
}
