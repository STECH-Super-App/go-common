package middleware_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

const grpcDurationFamily = "grpc_server_handling_seconds"

// Test (d): the unary interceptor records grpc_code for both a nil-error and
// an error return, and splits FullMethod into grpc_service / grpc_method
// (§3.2).
func TestMetricsUnaryServerInterceptor_RecordsCode(t *testing.T) {
	cases := []struct {
		name       string
		fullMethod string
		handlerErr error
		wantSvc    string
		wantMethod string
		wantCode   string
	}{
		{
			name:       "nil error is OK",
			fullMethod: "/users.v1.UserService/GetUserByID",
			handlerErr: nil,
			wantSvc:    "users.v1.UserService",
			wantMethod: "GetUserByID",
			wantCode:   "OK",
		},
		{
			name:       "status error carries its code",
			fullMethod: "/users.v1.UserService/GetUserByID",
			handlerErr: status.Error(codes.NotFound, "no such user"),
			wantSvc:    "users.v1.UserService",
			wantMethod: "GetUserByID",
			wantCode:   "NotFound",
		},
		{
			name:       "plain error is Unknown",
			fullMethod: "/media.v1.MediaService/GetMediaMetadataByID",
			handlerErr: assert.AnError,
			wantSvc:    "media.v1.MediaService",
			wantMethod: "GetMediaMetadataByID",
			wantCode:   "Unknown",
		},
	}

	interceptor := middleware.MetricsUnaryServerInterceptor()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{
				"grpc_service": tc.wantSvc,
				"grpc_method":  tc.wantMethod,
				"grpc_code":    tc.wantCode,
			}
			before := histCount(t, grpcDurationFamily, labels)

			resp, err := interceptor(
				context.Background(),
				"request",
				&grpc.UnaryServerInfo{FullMethod: tc.fullMethod},
				func(_ context.Context, _ any) (any, error) {
					return "response", tc.handlerErr
				},
			)

			if tc.handlerErr == nil {
				require.NoError(t, err)
				assert.Equal(t, "response", resp, "the interceptor must pass the handler result through")
			} else {
				require.Error(t, err, "the interceptor must not swallow the handler error")
			}

			assert.Equal(t, before+1, histCount(t, grpcDurationFamily, labels))
		})
	}
}

// A malformed FullMethod must not mint an unbounded label value.
func TestMetricsUnaryServerInterceptor_MalformedFullMethod(t *testing.T) {
	interceptor := middleware.MetricsUnaryServerInterceptor()

	labels := map[string]string{"grpc_service": "unknown", "grpc_method": "unknown", "grpc_code": "OK"}
	before := histCount(t, grpcDurationFamily, labels)

	_, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: ""},
		func(_ context.Context, _ any) (any, error) { return nil, nil },
	)
	require.NoError(t, err)

	assert.Equal(t, before+1, histCount(t, grpcDurationFamily, labels))
}

// Building the interceptor twice in one process must not panic (§3.0).
func TestMetricsUnaryServerInterceptor_BuiltTwiceDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		_ = middleware.MetricsUnaryServerInterceptor()
		_ = middleware.MetricsUnaryServerInterceptor()
	})
}
