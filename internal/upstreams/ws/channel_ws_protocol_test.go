package ws_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelWsProtocolRequestFrameStampsInternalId(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("header.Subscribe", []any{}, chains.CELESTIA)
	require.NoError(t, err)

	frame, err := wsProtocol.RequestFrame(request)
	require.NoError(t, err)

	assert.Equal(t, "101", frame.RequestId)
	assert.Equal(t, "header.Subscribe", frame.SubType)
	body := decodeBody(t, frame.Body)
	assert.Equal(t, "header.Subscribe", body["method"])
	assert.Equal(t, float64(101), body["id"])
	assert.Equal(t, []any{}, body["params"])
}

func TestChannelWsProtocolParseWsMessageChannelValue(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	response, err := wsProtocol.ParseWsMessage([]byte(`{"jsonrpc":"2.0","method":"xrpc.ch.val","params":[7,{"header":{"height":"42"}}]}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.Ws, response.Type)
	assert.Equal(t, "7", response.SubId)
	assert.Equal(t, []byte(`{"header":{"height":"42"}}`), response.Message)
	assert.Nil(t, response.Error)
	assert.Empty(t, response.Id)
}

// xrpc.ch.close is the node ending the subscription: a Ws frame carrying the
// total-failure error, which the registry and the engine treat as the end.
func TestChannelWsProtocolParseWsMessageChannelClose(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	response, err := wsProtocol.ParseWsMessage([]byte(`{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[7]}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.Ws, response.Type)
	assert.Equal(t, "7", response.SubId)
	assert.Equal(t, protocol.SubscribeTotalFailureError(), response.Error)
}

func TestChannelWsProtocolParseWsMessageSubscribeAck(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	response, err := wsProtocol.ParseWsMessage([]byte(`{"jsonrpc":"2.0","id":101,"result":7}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.JsonRpc, response.Type)
	assert.Equal(t, "101", response.Id)
	assert.Equal(t, []byte(`7`), response.Message)
	assert.Nil(t, response.Error)
}

func TestChannelWsProtocolParseWsMessageErrorResponse(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	response, err := wsProtocol.ParseWsMessage([]byte(`{"jsonrpc":"2.0","id":101,"error":{"code":1,"message":"missing permission"}}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.JsonRpc, response.Type)
	assert.Equal(t, "101", response.Id)
	require.NotNil(t, response.Error)
	assert.Equal(t, "missing permission", response.Error.Message)
}

func TestChannelWsProtocolParseWsMessageRejectsMalformedFrames(t *testing.T) {
	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")

	cases := map[string]string{
		`{"method":"xrpc.ch.val","params":[7]}`:           "xrpc.ch.val expects [channelId, value]",
		`{"method":"xrpc.ch.val","params":["7",{}]}`:      "channel id must be a number",
		`{"method":"xrpc.ch.close","params":[]}`:          "xrpc.ch.close expects [channelId]",
		`{"method":"xrpc.ch.close","params":[7,"extra"]}`: "xrpc.ch.close expects [channelId]",
		`not-json`: "invalid response type - unknown",
	}
	for payload, wantErr := range cases {
		response, err := wsProtocol.ParseWsMessage([]byte(payload))
		assert.Nil(t, response, payload)
		assert.ErrorContains(t, err, wantErr, payload)
	}
}

// xrpc.cancel takes the id of the subscribe request nodecore sent - the op id -
// never the channel id, and goes out as a notification without an id.
func TestChannelWsProtocolDoOnCloseFuncSendsCancelWithTheRequestId(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "header.Subscribe"
	requestOp.SubIDValue = "7"
	requestOp.IDValue = "101"

	var receivedBody []byte
	var deadline time.Time
	doOnClose := wsProtocol.DoOnCloseFunc(func(ctx context.Context, body []byte) error {
		receivedBody = append([]byte(nil), body...)
		var ok bool
		deadline, ok = ctx.Deadline()
		require.True(t, ok)
		return nil
	})

	doOnClose(requestOp)

	require.NotNil(t, receivedBody)
	assert.WithinDuration(t, time.Now().Add(5*time.Second), deadline, time.Second)
	body := decodeBody(t, receivedBody)
	assert.Equal(t, "2.0", body["jsonrpc"])
	assert.Equal(t, "xrpc.cancel", body["method"])
	assert.Equal(t, []any{float64(101)}, body["params"])
	_, hasId := body["id"]
	assert.False(t, hasId, "a cancel with an id only makes the node log a warning")
}

func TestChannelWsProtocolDoOnCloseFuncSkipsWithoutSubscriptionId(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "header.Subscribe"
	requestOp.IDValue = "101"

	called := false
	wsProtocol.DoOnCloseFunc(func(context.Context, []byte) error {
		called = true
		return nil
	})(requestOp)

	assert.False(t, called)
}

func TestChannelWsProtocolDoOnCloseFuncSkipsNonNumericRequestId(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewChannelWsProtocol("upstream-1", "celestia")
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "header.Subscribe"
	requestOp.SubIDValue = "7"
	requestOp.IDValue = "op-1"

	called := false
	wsProtocol.DoOnCloseFunc(func(context.Context, []byte) error {
		called = true
		return nil
	})(requestOp)

	assert.False(t, called)
}
