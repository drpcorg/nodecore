package emerald

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errSubscribeMappingNotSupported = errors.New("unsupported subscribe method mapping")

// jsonRpcNativeSubscribeAdapter serves JSON-RPC subscriptions: the dshackle
// topic (newHeads, logs, ...) or native sub method is mapped onto the chain's
// subscribe method, events are forwarded as bare payloads, and a failure ends
// the NativeSubscribe stream with a mapped gRPC status.
type jsonRpcNativeSubscribeAdapter struct{}

func (jsonRpcNativeSubscribeAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	chainSupervisor upstreams.ChainSupervisor,
	request *dshackle.NativeSubscribeRequest,
) (protocol.RequestHolder, error) {
	mappedMethod, mappedPayload, err := mapNativeSubscribeMethod(chain.MethodSpec, chainSupervisor, request.GetMethod(), request.GetPayload())
	if err != nil {
		if errors.Is(err, errSubscribeMappingNotSupported) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	jsonRpcRequestBody := protocol.JsonRpcRequestBody{Id: []byte("0"), Method: mappedMethod, Params: mappedPayload}
	selectors := mapDshackleSelectors([]*dshackle.Selector{request.GetSelector()})
	return protocol.NewUpstreamJsonRpcRequest("0", jsonRpcRequestBody, true, chain.MethodSpec, selectors...), nil
}

func (jsonRpcNativeSubscribeAdapter) SendReply(
	stream dshackle.Blockchain_NativeSubscribeServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
) (bool, error) {
	response := wrapper.Response
	if response.HasError() {
		return true, mapNativeSubscribeError(response.GetError())
	}
	// after the error check the flow only delivers events and the end frame; a
	// clean end has no JSON-RPC presentation, the stream just closes with OK
	if sub, ok := response.(protocol.SubscriptionResponseHolder); ok && sub.IsEnd() {
		return true, nil
	}

	replyItem, err := nativeSubscribeReplyItem(wrapper, response.ResponseResult(), nonce, signer)
	if err != nil {
		return true, err
	}
	return false, stream.Send(replyItem)
}

func mapNativeSubscribeMethod(
	methodSpecName string,
	chainSupervisor upstreams.ChainSupervisor,
	requestedMethod string,
	payload []byte,
) (string, []byte, error) {
	if supportsNativeSubscribeMethod(methodSpecName, requestedMethod) {
		return normalizeNativeSubscribePayload(requestedMethod, payload)
	}
	if !supportsEthSubscribeFallback(methodSpecName, chainSupervisor) {
		return "", nil, fmt.Errorf("%w: subscribe %s is not supported for chain spec %s", errSubscribeMappingNotSupported, requestedMethod, methodSpecName)
	}
	return mapToEthSubscribeFallback(requestedMethod, payload)
}

func supportsNativeSubscribeMethod(methodSpecName string, requestedMethod string) bool {
	return specs.IsSubscribeMethod(methodSpecName, requestedMethod)
}

func normalizeNativeSubscribePayload(requestedMethod string, payload []byte) (string, []byte, error) {
	if len(payload) == 0 {
		return requestedMethod, []byte("[]"), nil
	}
	if !json.Valid(payload) {
		return "", nil, fmt.Errorf("invalid subscribe payload format")
	}
	return requestedMethod, payload, nil
}

func supportsEthSubscribeFallback(methodSpecName string, chainSupervisor upstreams.ChainSupervisor) bool {
	ethSubscribeSupported := specs.IsSubscribeMethod(methodSpecName, "eth_subscribe")
	if !ethSubscribeSupported && chainSupervisor != nil {
		ethSubscribeSupported = chainSupervisor.GetMethod("eth_subscribe") != nil
	}
	return ethSubscribeSupported
}

func mapToEthSubscribeFallback(requestedMethod string, payload []byte) (string, []byte, error) {
	mappedParams, err := mapEthSubscribeParams(requestedMethod, payload)
	if err != nil {
		return "", nil, err
	}
	return "eth_subscribe", mappedParams, nil
}

func mapEthSubscribeParams(requestedMethod string, payload []byte) ([]byte, error) {
	methodRaw, _ := sonic.Marshal(requestedMethod)
	params := []json.RawMessage{methodRaw}

	// The dshackle client sends Method as the concrete subscription type and
	// Payload as that type's single parameter payload. For example:
	//   Method="logs", Payload={...} -> eth_subscribe params ["logs", {...}]
	//   Method="newHeads", Payload=null -> eth_subscribe params ["newHeads"]
	if len(payload) > 0 && string(payload) != "null" {
		if !json.Valid(payload) {
			return nil, fmt.Errorf("invalid subscribe payload format")
		}
		params = append(params, append(json.RawMessage(nil), payload...))
	}

	result, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mapNativeSubscribeError(responseError *protocol.ResponseError) error {
	if responseError == nil {
		return status.Error(codes.Internal, "internal server error")
	}

	switch responseError.Code {
	case protocol.NoAvailableUpstreams, protocol.WrongChain:
		return status.Error(codes.Unavailable, responseError.Message)
	case protocol.NoSupportedMethod:
		return status.Error(codes.Unimplemented, responseError.Message)
	case protocol.AuthErrorCode:
		return status.Error(codes.Unauthenticated, responseError.Message)
	default:
		if strings.Contains(strings.ToLower(responseError.Message), "subscription request") &&
			strings.Contains(strings.ToLower(responseError.Message), "unable to process") {
			return status.Error(codes.Unimplemented, responseError.Message)
		}
		return status.Error(codes.Internal, responseError.Message)
	}
}
