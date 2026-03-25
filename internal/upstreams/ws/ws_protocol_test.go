package ws_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loadSpecsOnce sync.Once

func TestJsonRpcWsProtocolRequestFrameForEthSubscription(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	frame, err := wsProtocol.RequestFrame(request)
	require.NoError(t, err)

	assert.Equal(t, "1", frame.RequestId)
	assert.Equal(t, "newHeads", frame.SubType)

	body := decodeBody(t, frame.Body)
	assert.Equal(t, "eth_subscribe", body["method"])
	assert.Equal(t, "1", body["id"])
	assert.Equal(t, []any{"newHeads"}, body["params"])
}

func TestJsonRpcWsProtocolRequestFrameForNonEthSubscription(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "solana", chains.SOLANA)

	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("logsSubscribe", []any{"mentions"}, chains.SOLANA)
	require.NoError(t, err)

	frame, err := wsProtocol.RequestFrame(request)
	require.NoError(t, err)

	assert.Equal(t, "1", frame.RequestId)
	assert.Equal(t, "logsSubscribe", frame.SubType)
}

func TestJsonRpcWsProtocolRequestFrameForUnaryRequest(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	firstRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	firstFrame, err := wsProtocol.RequestFrame(firstRequest)
	require.NoError(t, err)

	secondRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	require.NoError(t, err)

	secondFrame, err := wsProtocol.RequestFrame(secondRequest)
	require.NoError(t, err)

	assert.Equal(t, "1", firstFrame.RequestId)
	assert.Equal(t, "2", secondFrame.RequestId)
	assert.Empty(t, firstFrame.SubType)
	assert.Empty(t, secondFrame.SubType)
}

func TestJsonRpcWsProtocolDoOnCloseFuncSendsUnsubscribeRequest(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "eth_subscribe"
	requestOp.SubIDValue = "0xsub"

	var called bool
	var receivedBody []byte
	var deadline time.Time

	doOnClose := wsProtocol.DoOnCloseFunc(func(ctx context.Context, body []byte) error {
		called = true
		receivedBody = append([]byte(nil), body...)

		var ok bool
		deadline, ok = ctx.Deadline()
		require.True(t, ok)

		return nil
	})

	doOnClose(requestOp)

	require.True(t, called)
	assert.WithinDuration(t, time.Now().Add(5*time.Second), deadline, time.Second)

	body := decodeBody(t, receivedBody)
	assert.Equal(t, "eth_unsubscribe", body["method"])
	assert.Equal(t, "1", body["id"])
	assert.Equal(t, []any{"0xsub"}, body["params"])
}

func TestJsonRpcWsProtocolDoOnCloseFuncSkipsWithoutSubscriptionID(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "eth_subscribe"

	called := false
	doOnClose := wsProtocol.DoOnCloseFunc(func(ctx context.Context, body []byte) error {
		called = true
		return nil
	})

	doOnClose(requestOp)

	assert.False(t, called)
}

func TestJsonRpcWsProtocolDoOnCloseFuncSkipsWithoutUnsubscribeMapping(t *testing.T) {
	loadMethodSpecs(t)

	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)
	requestOp := mocks.NewRequestOperationMock()
	requestOp.MethodValue = "eth_blockNumber"
	requestOp.SubIDValue = "0xsub"

	called := false
	doOnClose := wsProtocol.DoOnCloseFunc(func(ctx context.Context, body []byte) error {
		called = true
		return nil
	})

	doOnClose(requestOp)

	assert.False(t, called)
}

func TestJsonRpcWsProtocolParseWsMessageForEvent(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	response, err := wsProtocol.ParseWsMessage([]byte(`{"id":"15","jsonrpc":"2.0","params":{"result":{"key":"value"},"subscription":"0xsub"}}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.Ws, response.Type)
	assert.Equal(t, "15", response.Id)
	assert.Equal(t, "0xsub", response.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), response.Message)
}

func TestJsonRpcWsProtocolParseWsMessageForJsonRpcResponse(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	response, err := wsProtocol.ParseWsMessage([]byte(`{"id":"7","jsonrpc":"2.0","result":true}`))
	require.NoError(t, err)

	assert.Equal(t, protocol.JsonRpc, response.Type)
	assert.Equal(t, "7", response.Id)
	assert.Empty(t, response.SubId)
	assert.Equal(t, []byte(`true`), response.Message)
}

func TestJsonRpcWsProtocolParseWsMessageInvalidPayload(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	response, err := wsProtocol.ParseWsMessage([]byte(`not-json`))

	assert.Nil(t, response)
	require.EqualError(t, err, "invalid response type - unknown")
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var parsed map[string]any
	require.NoError(t, sonic.Unmarshal(body, &parsed))

	return parsed
}

func loadMethodSpecs(t *testing.T) {
	t.Helper()

	loadSpecsOnce.Do(func() {
		require.NoError(t, specs.NewMethodSpecLoader().Load())
	})
}
