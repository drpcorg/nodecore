package grpc_ingress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// xNodecoreChain selects the target chain, sent as call metadata: the chain
// cannot ride the :path (gRPC owns it), and service names cannot identify
// chains. Auth reuses the existing X-Nodecore-Key/-Token headers as metadata.
// Metadata keys are case-insensitive (lowercase on the wire).
const xNodecoreChain = "X-Nodecore-Chain"

// NewServer builds the chain-ingress grpc.Server: the delegating codec (the
// reflection service's generated handlers keep the plain proto codec behind
// one type assertion; the catch-all gets raw bytes), the UnknownServiceHandler
// serving every unregistered method, chain-aware reflection, and keepalive
// enforcement so a client that opens a call and never sends is bounded even
// without its own deadline. baseOptions carry the shared transport options
// (tls). These options apply only to this server - the dshackle server never
// sees them.
func NewServer(appCtx *server_ctx.ApplicationServerContext, baseOptions ...grpc.ServerOption) *grpc.Server {
	ingress := newChainIngress(appCtx)
	options := append(baseOptions,
		grpc.ForceServerCodecV2(newDelegatingCodec()),
		grpc.UnknownServiceHandler(ingress.handle),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
			MaxConnectionIdle: 3 * time.Minute,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	server := grpc.NewServer(options...)
	registerChainAwareReflection(server)
	return server
}

// chainIngress is the catch-all handler of the chain-ingress gRPC server: a
// native gRPC call ("/sui.rpc.v2.LedgerService/GetObject") is routed through
// the same execution flow as JSON-RPC and REST, bytes-only. It fires for
// every service no registered handler owns - on this server only reflection
// is registered. The dshackle services live on their own server (grpc-port)
// and never pass through here.
type chainIngress struct {
	appCtx *server_ctx.ApplicationServerContext
}

func newChainIngress(appCtx *server_ctx.ApplicationServerContext) *chainIngress {
	return &chainIngress{appCtx: appCtx}
}

func (c *chainIngress) handle(_ any, stream grpc.ServerStream) error {
	fullMethod, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return status.Error(codes.Internal, "no method in the server stream")
	}
	// the emerald namespace belongs to the dshackle server on grpc-port; an
	// emerald method must never be proxied to a chain
	if strings.HasPrefix(fullMethod, "/emerald.") {
		return status.Errorf(codes.Unimplemented, "unknown method %s", fullMethod)
	}

	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	authPayload := auth.NewGrpcAuthPayload(md)
	if err := c.appCtx.AuthProcessor.Authenticate(ctx, authPayload); err != nil {
		return status.Errorf(codes.Unauthenticated, "auth error - %s", err.Error())
	}

	requestHandler := &grpcRequestHandler{stream: stream, md: md, method: fullMethod}
	handleResp := c.appCtx.HandleRequest(ctx, requestHandler, authPayload, flow.NewSubCtx())
	wrapper, ok := <-handleResp.ResponseWrappers()
	if !ok {
		return status.FromContextError(ctx.Err()).Err()
	}

	forwardResponseMetadata(stream, wrapper.Response)
	if wrapper.Response.HasError() {
		return grpcStatusFromResponseError(wrapper.Response.GetError()).Err()
	}
	return stream.SendMsg(&rawFrame{data: wrapper.Response.ResponseResult()})
}

// grpcRequestHandler decodes one unary gRPC call for the shared ingress
// pipeline: the chain from metadata, the call type from the method spec
// (arity is not on the gRPC wire), and exactly ONE request message - never
// waiting for the client's half-close (grpc-go's own processUnaryRPC does
// the same; extra frames from rogue clients are never read, HTTP/2 flow
// control bounds them).
type grpcRequestHandler struct {
	stream grpc.ServerStream
	md     metadata.MD
	method string
}

func (h *grpcRequestHandler) RequestDecode(_ context.Context) (*server_ctx.Request, error) {
	chainName := firstMetadataValue(h.md, xNodecoreChain)
	if chainName == "" {
		return nil, protocol.ResponseErrorWithData(protocol.ClientErrorCode, fmt.Sprintf("%s metadata is required", xNodecoreChain), nil)
	}
	// checked here too so an unknown chain answers "chain not supported"
	// instead of a misleading "unknown method" from an empty spec
	if !chains.IsSupported(chainName) {
		return nil, protocol.WrongChainError(chainName)
	}

	specName := chains.GetMethodSpecNameByChainName(chainName)
	specMethod := specs.GetSpecMethod(specName, h.method)
	if specMethod == nil {
		return nil, protocol.ResponseErrorWithData(protocol.NoSupportedMethod, fmt.Sprintf("unknown method %s", h.method), nil)
	}
	if specMethod.GrpcCallType() == specs.GrpcCallTypeServerStream {
		return nil, protocol.ResponseErrorWithData(protocol.NoSupportedMethod, "server-streaming methods are not supported yet", nil)
	}

	var requestFrame rawFrame
	if err := h.stream.RecvMsg(&requestFrame); err != nil {
		return nil, err
	}

	request := protocol.NewUpstreamGrpcRequest(
		"1",
		h.method,
		&protocol.RequestParams{Headers: h.md},
		requestFrame.data,
		specName,
	)
	return &server_ctx.Request{Chain: chainName, UpstreamRequests: []protocol.RequestHolder{request}}, nil
}

func (h *grpcRequestHandler) GetRequestType() protocol.RequestType {
	return protocol.Grpc
}

// forwardResponseMetadata forwards the upstream's filtered response metadata:
// headers via SetHeader (flushed with the first message or the status),
// trailers via SetTrailer - a gRPC client must receive trailers as trailers.
func forwardResponseMetadata(stream grpc.ServerStream, response protocol.ResponseHolder) {
	if headerBearer, ok := response.(protocol.HasResponseHeaders); ok {
		if headers := headerBearer.ResponseHeaders(); len(headers) > 0 {
			_ = stream.SetHeader(metadata.MD(headers))
		}
	}
	if trailerBearer, ok := response.(protocol.HasResponseTrailers); ok {
		if trailers := trailerBearer.ResponseTrailers(); len(trailers) > 0 {
			stream.SetTrailer(trailers)
		}
	}
}

// grpcStatusFromResponseError turns a flow error back into a *status.Status.
// An upstream gRPC status rides through verbatim - typed details included;
// nodecore's own error codes are mapped onto the closed 17-code model.
func grpcStatusFromResponseError(respError *protocol.ResponseError) *status.Status {
	if grpcStatus, ok := protocol.GrpcStatusFromError(respError); ok {
		if len(grpcStatus.StatusProto) > 0 {
			var statusProto spb.Status
			if err := proto.Unmarshal(grpcStatus.StatusProto, &statusProto); err == nil {
				return status.FromProto(&statusProto)
			}
		}
		return status.New(grpcStatus.Code, grpcStatus.Message)
	}

	var code codes.Code
	switch respError.Code {
	case protocol.NoAvailableUpstreams, protocol.NoApiConnectors:
		code = codes.Unavailable
	case protocol.AuthErrorCode:
		code = codes.PermissionDenied
	case protocol.ClientErrorCode, protocol.WrongChain:
		code = codes.InvalidArgument
	case protocol.RequestTimeout, protocol.CtxErrorCode:
		code = codes.DeadlineExceeded
	case protocol.RateLimitExceeded:
		code = codes.ResourceExhausted
	case protocol.NoSupportedMethod:
		code = codes.Unimplemented
	default:
		code = codes.Internal
	}
	return status.New(code, respError.Message)
}

func firstMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
