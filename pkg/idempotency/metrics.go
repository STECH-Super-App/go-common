package idempotency

import (
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// unclaimedResponses counts 2xx answers on routes registered through Add whose
// handler never ran a Claim (see Require). The label is the route pattern
// ("POST /orders/:id/accept"): bounded by the service's own route table, never
// user data. Any increase is a handler that does its work outside the key.
var unclaimedResponses = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "idempotency_unclaimed_responses_total",
	Help: "2xx answers on an idempotent route whose handler never ran an idempotency Claim, by route. Any increase means a retry with the same key would repeat the work.",
}, []string{"route"})

// keylessRequests counts calls to a route registered as Optional that carried no
// Idempotency-Key (see Optional). Its rate going to zero is the signal that the
// shipped clients have caught up and the route can be made required.
var keylessRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "idempotency_keyless_requests_total",
	Help: "Calls to an optional idempotent route without an Idempotency-Key, by route. When it stays flat, make the route required.",
}, []string{"route"})

func init() {
	metrics.Registry.MustRegister(unclaimedResponses, keylessRequests)
}

// unclaimedCount reads one route's series.
func unclaimedCount(route string) float64 {
	m := &dto.Metric{}
	if err := unclaimedResponses.WithLabelValues(route).Write(m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// keylessCount reads one route's keyless series.
func keylessCount(route string) float64 {
	m := &dto.Metric{}
	if err := keylessRequests.WithLabelValues(route).Write(m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}
