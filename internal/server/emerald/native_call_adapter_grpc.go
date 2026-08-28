package emerald

import (
	"fmt"
	"strconv"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
)

// grpcNativeCallAdapter serves a native unary gRPC call carried in
// NativeCallItem.grpc_data: the full method name in item.method, the
// serialized request message in payload (no wire frame prefix) and the call
// metadata to forward. The payload is never parsed.
type grpcNativeCallAdapter struct{}

func (a grpcNativeCallAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	item *dshackle.NativeCallItem,
	requestSelector *dshackle.Selector,
	_ uint32, // chunk_size: a unary gRPC reply is always one unchunked message
) (protocol.RequestHolder, *dshackle.NativeCallReplyItem) {
	grpcData := item.GetGrpcData()
	if grpcData == nil {
		return nil, a.ErrorItem(item.GetId(), protocol.ClientError(fmt.Errorf("grpc_data is missing")))
	}

	// arity is not on the gRPC wire - only the spec knows a method's shape.
	// Unknown methods answer precisely here instead of "no available upstreams",
	// and server streams belong to NativeSubscribe (the flow would route them
	// as subscriptions).
	specMethod := specs.GetSpecMethod(chain.MethodSpec, item.GetMethod())
	if specMethod == nil {
		return nil, a.ErrorItem(item.GetId(), protocol.NotSupportedMethodError(item.GetMethod()))
	}
	if specMethod.GrpcCallType().IsServerStream() {
		return nil, a.ErrorItem(item.GetId(),
			protocol.ClientError(fmt.Errorf("server-stream method %s must be called via NativeSubscribe", item.GetMethod())))
	}

	selectors, err := mapNativeCallSelectors(requestSelector, item.GetSelectors())
	if err != nil {
		return nil, a.ErrorItem(item.GetId(), protocol.ClientError(err))
	}

	requestID := strconv.FormatUint(uint64(item.GetId()), 10)
	requestParams := &protocol.RequestParams{
		Headers: server_ctx.SanitizeForwardedHeaders(keyValueListToMap(grpcData.GetMetadata())),
	}
	return protocol.NewUpstreamGrpcRequest(requestID, item.GetMethod(), requestParams, grpcData.GetPayload(), chain.MethodSpec, selectors...), nil
}

func (grpcNativeCallAdapter) SendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
) error {
	return sendReply(stream, wrapper, nonce, signer, passThroughStream, grpcNativeCallErrorItem)
}

func (grpcNativeCallAdapter) ErrorItem(requestID uint32, responseError *protocol.ResponseError) *dshackle.NativeCallReplyItem {
	replyItem := grpcNativeCallErrorItem(requestID, responseError, nil)
	replyItem.UpstreamId = flow.NoUpstream
	return replyItem
}

// grpcNativeCallErrorItem renders an error for a gRPC item: item_error_code is
// always a canonical gRPC code, error_message the status message, error_as_is
// the serialized google.rpc.Status when the upstream attached typed details.
// error_data is never used for gRPC items, and the "as is" body of other API
// kinds has no gRPC counterpart.
func grpcNativeCallErrorItem(requestID uint32, responseError *protocol.ResponseError, _ []byte) *dshackle.NativeCallReplyItem {
	grpcStatus := protocol.GrpcStatusOf(responseError)
	replyItem := &dshackle.NativeCallReplyItem{
		Id:            requestID,
		Succeed:       false,
		ErrorMessage:  grpcStatus.Message(),
		ItemErrorCode: int32(grpcStatus.Code()),
	}
	if upstreamStatus, ok := protocol.GrpcStatusFromError(responseError); ok && len(upstreamStatus.StatusProto) > 0 {
		replyItem.ErrorAsIs = append([]byte(nil), upstreamStatus.StatusProto...)
	}
	return replyItem
}
