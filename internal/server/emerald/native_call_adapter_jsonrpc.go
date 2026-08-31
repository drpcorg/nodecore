package emerald

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
)

type jsonRpcNativeCallAdapter struct{}

func (a jsonRpcNativeCallAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	item *dshackle.NativeCallItem,
	requestSelector *dshackle.Selector,
	chunkSize uint32,
) (protocol.RequestHolder, *dshackle.NativeCallReplyItem) {
	payload := item.GetPayload()
	if len(payload) == 0 {
		payload = []byte("[]")
	}
	if !json.Valid(payload) {
		return nil, withNoUpstreamId(a.ErrorItem(item.GetId(), protocol.ClientError(fmt.Errorf("payload is not a valid JSON value")), nil))
	}

	requestID := strconv.FormatUint(uint64(item.GetId()), 10)
	body := protocol.JsonRpcRequestBody{Id: []byte(requestID), Method: item.GetMethod(), Params: payload}
	selectors, err := mapNativeCallSelectors(requestSelector, item.GetSelectors())
	if err != nil {
		return nil, withNoUpstreamId(a.ErrorItem(item.GetId(), protocol.ClientError(err), nil))
	}
	if chunkSize > 0 {
		return protocol.NewStreamUpstreamJsonRpcRequest(requestID, body, chain.MethodSpec, selectors...), nil
	}
	return protocol.NewUpstreamJsonRpcRequest(requestID, body, false, chain.MethodSpec, selectors...), nil
}

func (jsonRpcNativeCallAdapter) SendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
) error {
	return sendReply(stream, wrapper, nonce, signer, unwrapJsonRpcResultStream, jsonRpcNativeCallAdapter{}.ErrorItem)
}

func (jsonRpcNativeCallAdapter) ErrorItem(requestID uint32, responseError *protocol.ResponseError, errorAsIs []byte) *dshackle.NativeCallReplyItem {
	return nativeCallErrorItem(requestID, responseError, errorAsIs)
}
