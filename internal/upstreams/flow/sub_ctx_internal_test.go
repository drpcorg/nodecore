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
	assert.True(t, subCtx.Exists("0x112"))

	response := subCtx.Unsubscribe(unsubscribeRequest("223", "eth_unsubscribe", `["0x112"]`, "eth"), "0x112")

	require.IsType(t, &UnaryResponse{}, response)
	wrapper := response.(*UnaryResponse).ResponseWrapper
	assert.Equal(t, NoUpstream, wrapper.UpstreamId)
	assert.Equal(t, "223", wrapper.RequestId)
	assert.False(t, wrapper.Response.HasError())
	assert.Equal(t, ResultTrue, wrapper.Response.ResponseResult())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.False(t, subCtx.Exists("0x112"))
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
	ctx := context.Background()

	first.Reserve(ctx, celestiaSubscribe(`"a"`))
	first.Reserve(ctx, celestiaSubscribe(`"b"`))
	second.Reserve(ctx, celestiaSubscribe(`"a"`))

	_, firstA, ok := first.attach("a")
	require.True(t, ok)
	_, firstB, ok := first.attach("b")
	require.True(t, ok)
	_, secondA, ok := second.attach("a")
	require.True(t, ok)
	assert.Equal(t, uint64(1), firstA)
	assert.Equal(t, uint64(2), firstB)
	assert.Equal(t, uint64(1), secondA)
}

// A client may reuse one request id for several subscribe calls; its
// xrpc.cancel with that id closes all of them, oldest first, one xrpc.ch.close
// each, and attach pairs each processor with the oldest unattached reservation.
func TestChannelSubCtxUnsubscribeClosesEveryChannelUnderTheRequestIdInOrder(t *testing.T) {
	subCtx := newChannelSubCtx()
	subCtx.Reserve(context.Background(), celestiaSubscribe(`"sd"`))
	subCtx.Reserve(context.Background(), celestiaSubscribe(`"sd"`))
	ctx1, id1, ok := subCtx.attach("sd")
	require.True(t, ok)
	ctx2, id2, ok := subCtx.attach("sd")
	require.True(t, ok)
	_, _, ok = subCtx.attach("sd")
	assert.False(t, ok, "both reservations are taken")
	assert.Equal(t, uint64(1), id1)
	assert.Equal(t, uint64(2), id2)

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
	assert.False(t, subCtx.Exists("sd"))
}

// The reservation is made when the ingress reads the subscribe frame, so a
// cancel that arrives before the processor attaches still finds it: the
// client gets its xrpc.ch.close and the processor later finds a cancelled
// context instead of opening a node subscription for nobody.
func TestChannelSubCtxCancelBeforeAttachIsHonoured(t *testing.T) {
	subCtx := newChannelSubCtx()
	subCtx.Reserve(context.Background(), celestiaSubscribe(`"sd"`))

	response := subCtx.Unsubscribe(unsubscribeRequest("224", "xrpc.cancel", `["sd"]`, "celestia"), "sd")

	closeFrame := <-response.(*SubscriptionResponse).ResponseWrappers
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[1]}`, subCtxEncoded(t, closeFrame))

	assert.True(t, subCtx.Exists("sd"), "kept for the processor that has not attached yet")
	ctx, channelId, ok := subCtx.attach("sd")
	require.True(t, ok)
	assert.Equal(t, uint64(1), channelId)
	assert.ErrorIs(t, ctx.Err(), context.Canceled, "the processor sees the cancel and opens nothing")
	assert.False(t, subCtx.Exists("sd"), "dropped once handed out")
	_, _, ok = subCtx.attach("sd")
	assert.False(t, ok)
}

// go-jsonrpc ignores a cancel for an unknown id, so nothing goes back.
func TestChannelSubCtxUnsubscribeUnknownIdAnswersNothing(t *testing.T) {
	subCtx := newChannelSubCtx()

	response := subCtx.Unsubscribe(unsubscribeRequest("224", "xrpc.cancel", `[404]`, "celestia"), "404")

	require.IsType(t, &SubscriptionResponse{}, response)
	_, open := <-response.(*SubscriptionResponse).ResponseWrappers
	assert.False(t, open)
}

// The reservation's context derives from the connection's: closing the
// connection ends every reserved subscription.
func TestChannelSubCtxReservationFollowsTheConnectionContext(t *testing.T) {
	subCtx := newChannelSubCtx()
	connCtx, closeConn := context.WithCancel(context.Background())
	subCtx.Reserve(connCtx, celestiaSubscribe(`1`))
	ctx, _, ok := subCtx.attach("1")
	require.True(t, ok)

	closeConn()

	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

// A request whose method has no JSON-RPC subscription block cannot be framed:
// the notification envelope needs its method name. Same guard on both
// dialects; a gRPC stream method is IsSubscribe without such a block.
func TestFramingsRefuseAMethodWithoutSubscriptionInfo(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	celestiaCall := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "header.LocalHead", Params: []byte(`[]`)}, true, "celestia")
	ethCall := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_blockNumber", Params: []byte(`[]`)}, true, "eth")

	channel := newChannelSubCtx()
	channel.Reserve(context.Background(), celestiaCall)
	_, err := channel.Framing().attach(context.Background(), celestiaCall)
	assert.EqualError(t, err, "header.LocalHead has no JSON-RPC subscription info")

	_, err = NewSubCtx(chains.ETHEREUM).Framing().attach(context.Background(), ethCall)
	assert.EqualError(t, err, "eth_blockNumber has no JSON-RPC subscription info")
}

func TestChannelFramingRefusesAnUnreservedRequest(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	_, err := newChannelSubCtx().Framing().attach(context.Background(), celestiaSubscribe(`"sd"`))
	assert.EqualError(t, err, "header.Subscribe was not reserved for request sd")
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
	assert.False(t, subCtx.Exists("0x112"))
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
	assert.True(t, subCtx.Exists("0x112"))
}

// The client subscribed with {"id":"sd"} and cancels with xrpc.cancel ["sd"]:
// the quotes of a string id must not get in the way of the lookup, and the
// reply is the channel's xrpc.ch.close, nothing else.
func TestLocalRequestProcessorXrpcCancelOverChannelSubCtx(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	subCtx := NewSubCtx(chains.CELESTIA).(*channelSubCtx)
	subCtx.Reserve(context.Background(), celestiaSubscribe(`"sd"`))
	ctx, channelId, ok := subCtx.attach("sd")
	require.True(t, ok)

	response := NewLocalRequestProcessor(chains.CELESTIA, subCtx).ProcessRequest(context.Background(), nil, unsubscribeRequest("224", "xrpc.cancel", `["sd"]`, "celestia"))

	require.IsType(t, &SubscriptionResponse{}, response)
	wrappers := response.(*SubscriptionResponse).ResponseWrappers
	closeFrame := <-wrappers
	assert.Equal(t, uint64(1), channelId)
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[1]}`, subCtxEncoded(t, closeFrame))
	_, open := <-wrappers
	assert.False(t, open)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.False(t, subCtx.Exists("sd"))
}

// celestiaSubscribe is a header.Subscribe request with the given raw JSON id.
func celestiaSubscribe(rawId string) protocol.RequestHolder {
	specs_utils.LoadMethodSpecs()
	return protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(rawId), Method: "header.Subscribe", Params: []byte(`[]`)}, true, "celestia")
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
