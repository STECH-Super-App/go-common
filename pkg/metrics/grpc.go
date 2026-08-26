package metrics

import "github.com/prometheus/client_golang/prometheus"

// GRPCServerHandling is the fleet-wide gRPC server histogram (§3.2).
//
//	grpc_server_handling_seconds{grpc_service, grpc_method, grpc_code}
//
// grpc_code is the canonical code string ("OK", "NotFound", …). It is observed
// by middleware.MetricsUnaryServerInterceptor(); services do not touch it
// directly.
//
// Unary covers the whole fleet today — no service serves a streaming RPC. The
// first streaming RPC added anywhere obliges a matching stream interceptor.
var GRPCServerHandling = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "grpc_server_handling_seconds",
		Help:    "gRPC server handling duration in seconds, by service, method and response code.",
		Buckets: DefaultBuckets,
	},
	[]string{"grpc_service", "grpc_method", "grpc_code"},
)

func init() {
	Registry.MustRegister(GRPCServerHandling)
}
