package grpc_ingress

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// grpcCall is one call shape of the chain ingress: how the request side is
// decoded for the shared pipeline (server_ctx.RequestHandler) and how the flow's
// response wrappers are written back to the stream. Unary and server-streaming
// share the one-frame decode (grpcRequestHandler) and differ in serve;
// client-streaming and bidi would add shapes here (and need HandleRequest
// changes - this is the seam, not readiness).
type grpcCall interface {
	server_ctx.RequestHandler
	serve(stream grpc.ServerStream, handleResp *server_ctx.HandleResponse) error
}

// unaryCall: exactly one response wrapper.
type unaryCall struct {
	grpcRequestHandler
}

// serverStreamCall: a sequence of event frames, then either a clean close (OK)
// or an error wrapper (its status).
type serverStreamCall struct {
	grpcRequestHandler
}

// newGrpcCall picks the shape from the method spec. Anything the spec does not
// know as a stream - including an unknown method or a missing chain - takes the
// unary shape, whose decode reports the precise error.
func newGrpcCall(stream grpc.ServerStream, md metadata.MD, fullMethod string) grpcCall {
	handler := grpcRequestHandler{stream: stream, md: md, method: fullMethod}
	chainName := firstMetadataValue(md, xNodecoreChain)
	if chainName != "" && chains.IsSupported(chainName) {
		specMethod := specs.GetSpecMethod(chains.GetMethodSpecNameByChainName(chainName), fullMethod)
		if specMethod != nil && specMethod.GrpcCallType().IsServerStream() {
			return &serverStreamCall{handler}
		}
	}
	return &unaryCall{handler}
}

func (u *unaryCall) serve(stream grpc.ServerStream, handleResp *server_ctx.HandleResponse) error {
	wrapper, ok := <-handleResp.ResponseWrappers()
	if !ok {
		return statusFromMissingResponse(stream.Context())
	}
	forwardResponseMetadata(stream, wrapper.Response)
	if wrapper.Response.HasError() {
		return protocol.GrpcStatusOf(wrapper.Response.GetError()).Err()
	}
	return stream.SendMsg(&rawFrame{data: wrapper.Response.ResponseResult()})
}

func (s *serverStreamCall) serve(stream grpc.ServerStream, handleResp *server_ctx.HandleResponse) error {
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case wrapper, ok := <-handleResp.ResponseWrappers():
			if !ok {
				// the flow closed the channel: a finite stream completed, or the
				// client went away (the flow drops responses on cancellation)
				if ctx.Err() != nil {
					return status.FromContextError(ctx.Err()).Err()
				}
				return nil
			}
			response := wrapper.Response
			// headers are flushed with the first SendMsg; SetHeader after that is
			// rejected by grpc-go and ignored here. Trailers accumulate until the
			// handler returns, so a trailer seen on any frame is delivered.
			forwardResponseMetadata(stream, response)
			if response.HasError() {
				return protocol.GrpcStatusOf(response.GetError()).Err()
			}
			if eventResponse, ok := response.(protocol.SubscriptionResponseHolder); ok && !eventResponse.IsEventFrame() {
				continue
			}
			if err := stream.SendMsg(&rawFrame{data: response.ResponseResult()}); err != nil {
				return err
			}
		}
	}
}
