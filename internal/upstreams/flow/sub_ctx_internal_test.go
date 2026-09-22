package flow

import (
	"context"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSubCtxPicksTheDialectByChainType(t *testing.T) {
	assert.IsType(t, &channelSubCtx{}, NewSubCtx(chains.CELESTIA))
	assert.IsType(t, &channelSubCtx{}, NewSubCtx(chains.CELESTIA_MOCHA))
	assert.IsType(t, &baseSubCtx{}, NewSubCtx(chains.ETHEREUM))

	assert.IsType(t, &jsonRpcFraming{}, NewSubCtx(chains.ETHEREUM).Framing())
	assert.IsType(t, &channelFraming{}, NewSubCtx(chains.CELESTIA).Framing())
	assert.IsType(t, resultOnlyFraming{}, NewResultOnlySubCtx().Framing())
}

func TestBaseSubCtxUnsubscribeCancelsAndAnswersTrue(t *testing.T) {
	subCtx := NewSubCtx(chains.ETHEREUM).(*baseSubCtx)
	ctx, cancel := context.WithCancel(context.Background())
	subCtx.addSub("0x112", cancel)
	_, filed := subCtx.subs.Load("0x112")
	assert.True(t, filed)

	response := subCtx.Unsubscribe(unsubscribeRequest("223", "eth_unsubscribe", `["0x112"]`, "eth"), "0x112")

	require.IsType(t, &UnaryResponse{}, response)
	wrapper := response.(*UnaryResponse).ResponseWrapper
	assert.Equal(t, NoUpstream, wrapper.UpstreamId)
	assert.Equal(t, "223", wrapper.RequestId)
	assert.False(t, wrapper.Response.HasError())
	assert.Equal(t, ResultTrue, wrapper.Response.ResponseResult())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	_, filed = subCtx.subs.Load("0x112")
	assert.False(t, filed)
}

// eth nodes answer true for an unknown subscription id as well.
func TestBaseSubCtxUnsubscribeUnknownIdStillAnswersTrue(t *testing.T) {
	subCtx := NewSubCtx(chains.ETHEREUM)

	response := subCtx.Unsubscribe(unsubscribeRequest("223", "eth_unsubscribe", `["0xmissing"]`, "eth"), "0xmissing")

	require.IsType(t, &UnaryResponse{}, response)
	assert.Equal(t, ResultTrue, response.(*UnaryResponse).ResponseWrapper.Response.ResponseResult())
}

func TestChannelSubCtxCountsChannelIdsPerConnection(t *testing.T) {
	first := newChannelSubCtx()
	second := newChannelSubCtx()

	assert.Equal(t, uint64(1), first.addSub("a", func() {}))
	assert.Equal(t, uint64(2), first.addSub("b", func() {}))
	assert.Equal(t, uint64(1), second.addSub("a", func() {}))
}

// A client may reuse one request id for several subscribe calls; its
// xrpc.cancel with that id closes all of them, oldest first, one xrpc.ch.close
// each.
func TestChannelSubCtxUnsubscribeClosesEveryChannelUnderTheRequestIdInOrder(t *testing.T) {
	subCtx := newChannelSubCtx()
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	subCtx.addSub("sd", cancel1)
	subCtx.addSub("sd", cancel2)

	response := subCtx.Unsubscribe(unsubscribeRequest("224", "xrpc.cancel", `["sd"]`, "celestia"), "sd")

	require.IsType(t, &SubscriptionResponse{}, response)
	wrappers := response.(*SubscriptionResponse).ResponseWrappers
	first := <-wrappers
	assert.Equal(t, NoUpstream, first.UpstreamId)
	assert.Equal(t, "224", first.RequestId)
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[1]}`, subCtxEncoded(t, first))
	second := <-wrappers
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[2]}`, subCtxEncoded(t, second))
	_, open := <-wrappers
	assert.False(t, open)
	assert.ErrorIs(t, ctx1.Err(), context.Canceled)
	assert.ErrorIs(t, ctx2.Err(), context.Canceled)
	assert.NotContains(t, subCtx.subs, "sd")
}

// go-jsonrpc ignores a cancel for an unknown id, so nothing goes back.
func TestChannelSubCtxUnsubscribeUnknownIdAnswersNothing(t *testing.T) {
	subCtx := newChannelSubCtx()

	response := subCtx.Unsubscribe(unsubscribeRequest("224", "xrpc.cancel", `[404]`, "celestia"), "404")

	require.IsType(t, &SubscriptionResponse{}, response)
	_, open := <-response.(*SubscriptionResponse).ResponseWrappers
	assert.False(t, open)
}

// A request whose method has no JSON-RPC subscription block cannot be framed:
// the notification envelope needs its method name. Same guard on both
// dialects; a gRPC stream method is IsSubscribe without such a block.
func TestFramingsRefuseAMethodWithoutSubscriptionInfo(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	celestiaCall := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "header.LocalHead", Params: []byte(`[]`)}, true, "celestia")
	ethCall := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_blockNumber", Params: []byte(`[]`)}, true, "eth")

	ack, err := newChannelSubCtx().Framing().begin(celestiaCall, func() {})
	assert.Nil(t, ack)
	assert.EqualError(t, err, "header.LocalHead has no JSON-RPC subscription info")

	ack, err = NewSubCtx(chains.ETHEREUM).Framing().begin(ethCall, func() {})
	assert.Nil(t, ack)
	assert.EqualError(t, err, "eth_blockNumber has no JSON-RPC subscription info")
}

func TestLocalRequestProcessorUnsubscribeOverBaseSubCtx(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	subCtx := NewSubCtx(chains.ALEPHZERO).(*baseSubCtx)
	ctx, cancel := context.WithCancel(context.Background())
	subCtx.addSub("0x112", cancel)

	response := NewLocalRequestProcessor(chains.ALEPHZERO, subCtx).ProcessRequest(context.Background(), nil, unsubscribeRequest("223", "eth_unsubscribe", `["0x112"]`, "eth"))

	require.IsType(t, &UnaryResponse{}, response)
	wrapper := response.(*UnaryResponse).ResponseWrapper
	assert.Equal(t, "223", wrapper.RequestId)
	assert.Equal(t, ResultTrue, wrapper.Response.ResponseResult())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	_, filed := subCtx.subs.Load("0x112")
	assert.False(t, filed)
}

func TestLocalRequestProcessorUnsubscribeWithoutParamsIsAnErrorAndKeepsTheSub(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	subCtx := NewSubCtx(chains.POLYGON).(*baseSubCtx)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subCtx.addSub("0x112", cancel)

	response := NewLocalRequestProcessor(chains.POLYGON, subCtx).ProcessRequest(context.Background(), nil, unsubscribeRequest("223", "eth_unsubscribe", "", "eth"))

	require.IsType(t, &UnaryResponse{}, response)
	wrapper := response.(*UnaryResponse).ResponseWrapper
	assert.True(t, wrapper.Response.HasError())
	assert.ErrorContains(t, wrapper.Response.GetError(), "internal server error")
	assert.NoError(t, ctx.Err())
	_, filed := subCtx.subs.Load("0x112")
	assert.True(t, filed)
}

// The client subscribed with {"id":"sd"} and cancels with xrpc.cancel ["sd"]:
// the quotes of a string id must not get in the way of the lookup, and the
// reply is the channel's xrpc.ch.close, nothing else.
func TestLocalRequestProcessorXrpcCancelOverChannelSubCtx(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	subCtx := NewSubCtx(chains.CELESTIA).(*channelSubCtx)
	ctx, cancel := context.WithCancel(context.Background())
	subscribe := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`"sd"`), Method: "header.Subscribe", Params: []byte(`[]`)}, true, "celestia")
	channelId := subCtx.addSub(subscribe.RealId(), cancel)

	response := NewLocalRequestProcessor(chains.CELESTIA, subCtx).ProcessRequest(context.Background(), nil, unsubscribeRequest("224", "xrpc.cancel", `["sd"]`, "celestia"))

	require.IsType(t, &SubscriptionResponse{}, response)
	wrappers := response.(*SubscriptionResponse).ResponseWrappers
	closeFrame := <-wrappers
	assert.Equal(t, uint64(1), channelId)
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[1]}`, subCtxEncoded(t, closeFrame))
	_, open := <-wrappers
	assert.False(t, open)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.NotContains(t, subCtx.subs, "sd")
}

func unsubscribeRequest(id, method, params, spec string) protocol.RequestHolder {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: method}
	if params != "" {
		body.Params = []byte(params)
	}
	return protocol.NewUpstreamJsonRpcRequest(id, body, false, spec)
}

func subCtxEncoded(t *testing.T, wrapper *protocol.ResponseHolderWrapper) string {
	t.Helper()
	encoded, err := io.ReadAll(wrapper.Response.EncodeResponse([]byte(`1`)))
	require.NoError(t, err)
	return string(encoded)
}
