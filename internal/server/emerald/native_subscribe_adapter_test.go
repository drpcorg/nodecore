package emerald

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubscribeAdapterForPicksJsonRpcForNonStreamMethods(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	assert.IsType(t, jsonRpcNativeSubscribeAdapter{}, subscribeAdapterFor("eth", "newHeads"))
	assert.IsType(t, jsonRpcNativeSubscribeAdapter{}, subscribeAdapterFor("eth", "eth_subscribe"))
	assert.IsType(t, jsonRpcNativeSubscribeAdapter{}, subscribeAdapterFor("solana", "slotSubscribe"))
	// unknown method: the JSON-RPC adapter reports the precise mapping error
	assert.IsType(t, jsonRpcNativeSubscribeAdapter{}, subscribeAdapterFor("eth", "nope"))
}

func TestSubscribeAdapterForPicksGrpcForServerStreams(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	assert.IsType(t, grpcNativeSubscribeAdapter{}, subscribeAdapterFor("sui", "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints"))
	assert.IsType(t, grpcNativeSubscribeAdapter{}, subscribeAdapterFor("sui", "/sui.rpc.v2.LedgerService/ListCheckpoints"))
	// a unary gRPC method is not a subscription: the JSON-RPC adapter reports the mapping error
	assert.IsType(t, jsonRpcNativeSubscribeAdapter{}, subscribeAdapterFor("sui", "/sui.rpc.v2.LedgerService/GetServiceInfo"))
}

func TestJsonRpcSubscribeAdapterBuildsSubscriptionRequest(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	adapter := jsonRpcNativeSubscribeAdapter{}
	chain := &chains.ConfiguredChain{Chain: chains.ETHEREUM, MethodSpec: "eth"}

	request, err := adapter.BuildRequest(chain, nil, &dshackle.NativeSubscribeRequest{
		Method:   "newHeads",
		Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us"}}}},
	})
	require.NoError(t, err)

	assert.Equal(t, protocol.JsonRpc, request.RequestType())
	assert.Equal(t, "eth_subscribe", request.Method())
	assert.True(t, request.IsSubscribe())
	assert.Equal(t, "0", request.Id())
	body, err := request.Body()
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":0,"jsonrpc":"2.0","method":"eth_subscribe","params":["newHeads"]}`, string(body))
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}}, request.Selectors())
}

func TestJsonRpcSubscribeAdapterMapsBuildErrorsToStatuses(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	adapter := jsonRpcNativeSubscribeAdapter{}

	// a nil chain supervisor: the eth_subscribe fallback probe is guarded and
	// the spec alone decides (same as TestMapNativeSubscribeMethod)
	_, err := adapter.BuildRequest(&chains.ConfiguredChain{MethodSpec: "solana"}, nil, &dshackle.NativeSubscribeRequest{Method: "newHeads"})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))

	_, err = adapter.BuildRequest(&chains.ConfiguredChain{MethodSpec: "eth"}, nil, &dshackle.NativeSubscribeRequest{Method: "eth_subscribe", Payload: []byte("not-json")})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestJsonRpcSubscribeAdapterSendReply(t *testing.T) {
	adapter := jsonRpcNativeSubscribeAdapter{}
	signer := signature.NewDisabledSigner()

	t.Run("event becomes a bare item without grpc_data", func(te *testing.T) {
		stream := &testNativeSubscribeStream{ctx: context.Background()}
		wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "eth-1", RequestId: "0", Response: protocol.NewSubscriptionEventResponse("0", []byte(`{"number":"0x1"}`))}

		done, err := adapter.SendReply(stream, wrapper, 0, signer)
		require.NoError(te, err)
		assert.False(te, done)
		require.Len(te, stream.sent, 1)
		assert.Equal(te, `{"number":"0x1"}`, string(stream.sent[0].GetPayload()))
		assert.Equal(te, "eth-1", stream.sent[0].GetUpstreamId())
		assert.Nil(te, stream.sent[0].GetGrpcData())
		assert.Nil(te, stream.sent[0].GetSignature())
	})

	t.Run("end frame ends the stream cleanly", func(te *testing.T) {
		stream := &testNativeSubscribeStream{ctx: context.Background()}
		wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "eth-1", RequestId: "0", Response: protocol.NewSubscriptionEndResponse("0")}

		done, err := adapter.SendReply(stream, wrapper, 0, signer)
		require.NoError(te, err)
		assert.True(te, done)
		assert.Empty(te, stream.sent, "a JSON-RPC subscription has no in-band end item")
	})

	t.Run("error ends the stream with a mapped status", func(te *testing.T) {
		stream := &testNativeSubscribeStream{ctx: context.Background()}
		wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "eth-1", RequestId: "0", Response: protocol.NewReplyError("0", protocol.NoAvailableUpstreamsError(), protocol.JsonRpc, protocol.TotalFailure)}

		done, err := adapter.SendReply(stream, wrapper, 0, signer)
		assert.True(te, done)
		require.Error(te, err)
		assert.Equal(te, codes.Unavailable, status.Code(err))
		assert.Empty(te, stream.sent)
	})
}
