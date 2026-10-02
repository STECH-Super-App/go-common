package input

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

// GRPC turns validation failures into a gRPC status error: code
// InvalidArgument, the first failure's Reason as the status message, and every
// field in an errdetails.BadRequest (Field, Description = Reason, Reason).
// It returns nil when fields is empty, so a handler can write
//
//	if err := input.GRPC(input.Collect(a.At("name"), b.At("phone"))...); err != nil {
//		return nil, err
//	}
//
// This is the input half of go-common#75 only; it does not map AppErrors.
func GRPC(fields ...commonErrors.FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	violations := make([]*errdetails.BadRequest_FieldViolation, 0, len(fields))
	for _, f := range fields {
		violations = append(violations, &errdetails.BadRequest_FieldViolation{
			Field:       f.Field,
			Description: f.Reason,
			Reason:      f.Reason,
		})
	}
	st := status.New(codes.InvalidArgument, fields[0].Reason)
	withDetails, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: violations})
	if err != nil {
		// WithDetails fails only if the detail cannot be marshalled into an
		// Any, which a generated BadRequest always can; keep the code and
		// message rather than lose the error.
		return st.Err()
	}
	return withDetails.Err()
}
