package clientip

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// outgoing returns ctx ready for a call: any hand-written client-address
// metadata is removed and, when ctx carries an address, the validated one is
// written. Overwriting rather than appending keeps a caller from sending two.
func outgoing(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy() // never mutate the caller's metadata
	md.Delete(MetadataKey)
	if addr, ok := FromContext(ctx); ok {
		md.Set(MetadataKey, addr.String())
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// UnaryClientInterceptor writes the client address parked on the calling
// context into outgoing metadata under MetadataKey. With no address on the
// context nothing is sent.
//
// Attach with grpc.WithChainUnaryInterceptor, beside requestid.ClientInterceptor.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(outgoing(ctx), method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor is UnaryClientInterceptor for streaming calls.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(outgoing(ctx), desc, cc, method, opts...)
	}
}

// fromIncoming reads and validates the address in incoming metadata: exactly
// one value, a valid address.
func fromIncoming(ctx context.Context) (context.Context, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, false
	}
	values := md.Get(MetadataKey)
	if len(values) != 1 {
		return ctx, false
	}
	addr, ok := Parse(values[0])
	if !ok {
		return ctx, false
	}
	return NewContext(ctx, addr), true
}

// UnaryServerInterceptor moves the validated client address from incoming
// metadata onto the handler's context (read it with FromContext). A missing or
// invalid value leaves the context untouched; the call is never refused.
//
// Attach with grpc.ChainUnaryInterceptor, beside requestid.UnaryServerInterceptor.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, _ = fromIncoming(ctx)
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is UnaryServerInterceptor for streaming calls.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if ctx, ok := fromIncoming(ss.Context()); ok {
			ss = &contextStream{ServerStream: ss, ctx: ctx}
		}
		return handler(srv, ss)
	}
}

// contextStream is a ServerStream whose Context is replaced.
type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextStream) Context() context.Context { return s.ctx }
