package middleware

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// grpcLabelUnknown is the clamp for a FullMethod that does not parse. gRPC
// always supplies "/package.Service/Method", so this is defence against a
// malformed value minting an unbounded label, never an expected path.
const grpcLabelUnknown = "unknown"

// MetricsUnaryServerInterceptor returns the fleet-wide gRPC server metrics
// interceptor. It observes metrics.GRPCServerHandling —
// grpc_server_handling_seconds{grpc_service, grpc_method, grpc_code} — for
// every unary call (§3.2).
//
// Attach it with grpc.ChainUnaryInterceptor, never grpc.UnaryInterceptor:
// grpc-go permits the latter only once per server, and machinery-service
// already chains four interceptors.
//
//	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(middleware.MetricsUnaryServerInterceptor()))
//
// It never swallows or rewrites the handler's error; it only measures.
func MetricsUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		service, method := splitFullMethod(info.FullMethod)
		metrics.GRPCServerHandling.WithLabelValues(
			service,
			method,
			status.Code(err).String(),
		).Observe(time.Since(start).Seconds())

		return resp, err
	}
}

// splitFullMethod splits gRPC's "/package.Service/Method" into its two label
// values, clamping anything unparseable to a fixed string.
func splitFullMethod(fullMethod string) (service, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")

	sep := strings.LastIndex(trimmed, "/")
	if sep <= 0 || sep == len(trimmed)-1 {
		return grpcLabelUnknown, grpcLabelUnknown
	}

	return trimmed[:sep], trimmed[sep+1:]
}
