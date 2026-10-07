package hashicorp

import (
	"context"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const grpcServiceName = "ctx.plugin.v1.Runtime"
const grpcMaxMessage = plugin.MaxFrameBytes + 1024 // protobuf envelope overhead

// GRPCServer is a go-plugin ServeConfig.GRPCServer factory with explicit wire
// limits. It preserves upstream TLS options. JSON inside BytesValue remains
// bounded separately to MaxFrameBytes on both sides.
func GRPCServer(options []grpc.ServerOption) *grpc.Server {
	options = append(options, grpc.MaxRecvMsgSize(grpcMaxMessage), grpc.MaxSendMsgSize(grpcMaxMessage))
	return grpc.NewServer(options...)
}

type grpcBackend struct {
	conn     *grpc.ClientConn
	lifetime context.Context
	cancel   context.CancelFunc
}

func (b *grpcBackend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	var response wrapperspb.BytesValue
	var descriptor plugin.Descriptor
	err := b.call(ctx, "Handshake", &emptypb.Empty{}, &response, time.Time{})
	if err == nil {
		err = plugin.Decode(response.Value, &descriptor)
	}
	if err == nil {
		err = descriptor.Validate()
	}
	return descriptor, err
}

func (b *grpcBackend) Invoke(ctx context.Context, request plugin.Request) (plugin.Response, error) {
	data, err := encode(request)
	if err != nil {
		return plugin.Response{}, err
	}
	var raw wrapperspb.BytesValue
	var response plugin.Response
	err = b.call(ctx, "Invoke", wrapperspb.Bytes(data), &raw, request.Deadline)
	if err == nil {
		err = plugin.Decode(raw.Value, &response)
	}
	if err == nil {
		err = response.Validate(request.ID)
	}
	return response, err
}

func (b *grpcBackend) call(ctx context.Context, method string, request, response interface{}, deadline time.Time) error {
	if b.lifetime.Err() != nil {
		return plugin.ErrClosed
	}
	ctx, cancel := bounded(ctx, deadline)
	defer cancel()
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	err := b.conn.Invoke(ctx, "/"+grpcServiceName+"/"+method, request, response, grpc.MaxCallRecvMsgSize(grpcMaxMessage), grpc.MaxCallSendMsgSize(grpcMaxMessage))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Close cancels this binding's calls, retaining a shared go-plugin connection.
// Connect's owned backend additionally asks the upstream client to kill its
// process. Direct Dispense users retain ownership of Client.Kill themselves.
func (b *grpcBackend) Close() error { b.cancel(); return nil }

type grpcGuest struct{ guest plugin.Endpoint }

func (s *grpcGuest) Handshake(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	d, err := s.guest.Handshake(ctx)
	if err != nil {
		return nil, status.Error(codes.Canceled, "plugin handshake canceled")
	}
	data, err := encode(d)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid plugin descriptor")
	}
	return wrapperspb.Bytes(data), nil
}

func (s *grpcGuest) Invoke(ctx context.Context, raw *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	var request plugin.Request
	if err := plugin.Decode(raw.Value, &request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plugin request")
	}
	response, err := s.guest.Invoke(ctx, request)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid plugin response")
	}
	data, err := encode(response)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid plugin response")
	}
	return wrapperspb.Bytes(data), nil
}

// The service uses standard protobuf Empty and BytesValue messages, so no
// generated Go message types or custom gRPC codecs are needed. runtime.proto
// is the language-neutral service definition for other host/guest SDKs.
type runtimeServer interface {
	Handshake(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error)
	Invoke(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}

var runtimeService = grpc.ServiceDesc{
	ServiceName: grpcServiceName,
	HandlerType: (*runtimeServer)(nil),
	Metadata:    "runtime.proto",
	Methods: []grpc.MethodDesc{
		{MethodName: "Handshake", Handler: func(server interface{}, ctx context.Context, decode func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
			request := new(emptypb.Empty)
			if err := decode(request); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, value interface{}) (interface{}, error) {
				return server.(runtimeServer).Handshake(ctx, value.(*emptypb.Empty))
			}
			if interceptor == nil {
				return handler(ctx, request)
			}
			return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: server, FullMethod: "/" + grpcServiceName + "/Handshake"}, handler)
		}},
		{MethodName: "Invoke", Handler: func(server interface{}, ctx context.Context, decode func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
			request := new(wrapperspb.BytesValue)
			if err := decode(request); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, value interface{}) (interface{}, error) {
				return server.(runtimeServer).Invoke(ctx, value.(*wrapperspb.BytesValue))
			}
			if interceptor == nil {
				return handler(ctx, request)
			}
			return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: server, FullMethod: "/" + grpcServiceName + "/Invoke"}, handler)
		}},
	},
}
