package emerald

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// grpcNativeSubscribeAdapter serves a gRPC server-streaming method carried by
// NativeSubscribe: request.method is the full method name, payload the
// serialized request message (no wire frame prefix, never parsed) and
// grpc_data.metadata the call metadata to forward. The upstream's metadata,
// trailers and terminal status ride in-band in grpc_data, set only on the
// items that have something to carry (the first, the final); the
// NativeSubscribe stream itself closes with OK.
type grpcNativeSubscribeAdapter struct{}

func (grpcNativeSubscribeAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	_ upstreams.ChainSupervisor,
	request *dshackle.NativeSubscribeRequest,
) (protocol.RequestHolder, error) {
	// Only a server-streaming gRPC method is a subscription here: anything else
	// would be built as a non-subscription request and routed to the unary flow
	// (the mirror of the NativeCall gRPC adapter, which refuses streams). A gRPC
	// call type also implies the grpc connector - spec validation ties them.
	specMethod := specs.GetSpecMethod(chain.MethodSpec, request.GetMethod())
	if specMethod == nil || !specMethod.GrpcCallType().IsServerStream() {
		return nil, status.Error(codes.Unimplemented, protocol.NotSupportedMethodError(request.GetMethod()).Message)
	}

	requestParams := &protocol.RequestParams{
		Headers: server_ctx.SanitizeForwardedHeaders(keyValueListToMap(request.GetGrpcData().GetMetadata())),
	}
	selectors := mapDshackleSelectors([]*dshackle.Selector{request.GetSelector()})
	return protocol.NewUpstreamGrpcRequest("0", request.GetMethod(), requestParams, request.GetPayload(), chain.MethodSpec, selectors...), nil
}

func (grpcNativeSubscribeAdapter) SendReply(
	stream dshackle.Blockchain_NativeSubscribeServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
) (bool, error) {
	response := wrapper.Response

	if response.HasError() {
		// every terminal failure - the node's own status, verbatim with details,
		// or a nodecore failure on a canonical code - is one google.rpc.Status
		grpcStatus, _ := protocol.GrpcStatusOf(response.GetError())
		statusBytes, err := proto.Marshal(grpcStatus.Proto())
		if err != nil {
			return true, status.Error(codes.Internal, "unable to encode the stream status")
		}
		return true, stream.Send(grpcFinalItem(wrapper.UpstreamId, response, statusBytes))
	}

	// after the error check the flow only delivers events and the end frame;
	// the assertion exists because IsEnd lives on the narrower interface
	if sub, ok := response.(protocol.SubscriptionResponseHolder); ok && sub.IsEnd() {
		return true, stream.Send(grpcFinalItem(wrapper.UpstreamId, response, nil))
	}

	replyItem, err := nativeSubscribeReplyItem(wrapper, response.ResponseResult(), nonce, signer)
	if err != nil {
		return true, err
	}
	// the flow puts the upstream headers only on the frame that carried them
	// (the first), so a data item has grpc_data exactly when it has metadata
	if grpcData := grpcSubResponseData(response); grpcData != nil {
		replyItem.Data = &dshackle.NativeSubscribeReplyItem_GrpcData{GrpcData: grpcData}
	}
	return false, stream.Send(replyItem)
}

// grpcSubResponseData carries the upstream metadata a wrapper has, or nil when
// there is none to carry.
func grpcSubResponseData(response protocol.ResponseHolder) *dshackle.GrpcSubResponseData {
	headers, trailers := protocol.ResponseMetadata(response)
	if len(headers) == 0 && len(trailers) == 0 {
		return nil
	}
	return &dshackle.GrpcSubResponseData{
		Metadata: mapHeaders(headers),
		Trailers: mapHeaders(trailers),
	}
}

// grpcFinalItem is the terminal item of a gRPC stream: no payload, never
// signed, final set, the trailers (and the headers of a zero-message stream)
// the upstream closed with, statusBytes empty for a clean end.
func grpcFinalItem(upstreamId string, response protocol.ResponseHolder, statusBytes []byte) *dshackle.NativeSubscribeReplyItem {
	headers, trailers := protocol.ResponseMetadata(response)
	return &dshackle.NativeSubscribeReplyItem{
		UpstreamId: upstreamId,
		Data: &dshackle.NativeSubscribeReplyItem_GrpcData{GrpcData: &dshackle.GrpcSubResponseData{
			Metadata: mapHeaders(headers),
			Trailers: mapHeaders(trailers),
			Final:    true,
			Status:   statusBytes,
		}},
	}
}

var _ nativeSubscribeAdapter = grpcNativeSubscribeAdapter{}
