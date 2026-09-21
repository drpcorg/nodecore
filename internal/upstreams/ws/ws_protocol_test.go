package ws_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonRpcWsProtocolRequestFrameForEthSubscription(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	frame, err := wsProtocol.RequestFrame(request)
	require.NoError(t, err)

	// Allocated ids start above the reserved low range so they can never collide
	// with the id 1 that internally-built frames (e.g. unsubscribe) go out with.
	assert.Equal(t, "101", frame.RequestId)
	assert.Equal(t, "newHeads", frame.SubType)

	// The id is a JSON number on the wire but a decimal string in the registry
	// maps - asserting both here pins the invariant the id routing relies on.
	assert.Contains(t, string(frame.Body), `"id":101`)

	body := decodeBody(t, frame.Body)
	assert.Equal(t, "eth_subscribe", body["method"])
	assert.Equal(t, float64(101), body["id"])
	assert.Equal(t, []any{"newHeads"}, body["params"])
}

func TestJsonRpcWsProtocolRequestFrameForNonEthSubscription(t *testing.T) {
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "solana", chains.SOLANA)

	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("logsSubscribe", []any{"mentions"}, chains.SOLANA)
	require.NoError(t, err)

	frame, err := wsProtocol.RequestFrame(request)
	require.NoError(t, err)

	assert.Equal(t, "101", frame.RequestId)
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

	assert.Equal(t, "101", firstFrame.RequestId)
	assert.Equal(t, "102", secondFrame.RequestId)
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

	// The unsubscribe body is written straight to the socket without going
	// through RequestFrame, so its numeric id comes from the internal request
	// constructor - chains that reject string ids would reject this frame too.
	// It stays 1, in the range the allocator reserves, so its reply can never be
	// mistaken for the response to an allocated request.
	assert.Contains(t, string(receivedBody), `"id":1`)

	body := decodeBody(t, receivedBody)
	assert.Equal(t, "eth_unsubscribe", body["method"])
	assert.Equal(t, float64(1), body["id"])
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

// The non-UTF-8 rejection case lives in ws_protocol_utf8_test.go (internal test
// package) so it can assert the sentinel with errors.Is.

// Regression: Raw() on a number node returns "1", so the old sub[1:len(sub)-1] was
// sub[1:0] and panicked with "slice bounds out of range [1:0]". A non-string first
// param is not a rejection reason - only invalid UTF-8 is - so it falls back to the
// method name and the upstream gets to reject the request on its own terms.
func TestJsonRpcWsProtocolRequestFrameNumberSubTypeFallsBackToMethod(t *testing.T) {
	loadMethodSpecs(t)
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{
		Id:      json.RawMessage(`1`),
		Jsonrpc: "2.0",
		Method:  "eth_subscribe",
		Params:  json.RawMessage(`[1]`),
	}, true, "eth")

	frame, err := wsProtocol.RequestFrame(request)

	require.NoError(t, err)
	require.NotNil(t, frame)
	assert.Equal(t, "eth_subscribe", frame.SubType)
}

// Same guard, other direction: an object first param used to yield the label
// `"a":1` because Raw() returned {"a":1} and the slice just chopped the braces.
func TestJsonRpcWsProtocolRequestFrameObjectSubTypeFallsBackToMethod(t *testing.T) {
	loadMethodSpecs(t)
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{
		Id:      json.RawMessage(`1`),
		Jsonrpc: "2.0",
		Method:  "eth_subscribe",
		Params:  json.RawMessage(`[{"a":1}]`),
	}, true, "eth")

	frame, err := wsProtocol.RequestFrame(request)

	require.NoError(t, err)
	require.NotNil(t, frame)
	assert.Equal(t, "eth_subscribe", frame.SubType)
}

// Missing params: GetByPath returns a non-existent node, which must not be treated
// as a subscription type.
func TestJsonRpcWsProtocolRequestFrameEmptyParamsFallsBackToMethod(t *testing.T) {
	loadMethodSpecs(t)
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{
		Id:      json.RawMessage(`1`),
		Jsonrpc: "2.0",
		Method:  "eth_subscribe",
		Params:  json.RawMessage(`[]`),
	}, true, "eth")

	frame, err := wsProtocol.RequestFrame(request)

	require.NoError(t, err)
	require.NotNil(t, frame)
	assert.Equal(t, "eth_subscribe", frame.SubType)
}

// A well-formed string subscription type keeps working unchanged.
func TestJsonRpcWsProtocolRequestFrameValidSubTypeUnchanged(t *testing.T) {
	loadMethodSpecs(t)
	wsProtocol := ws.NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{
		Id:      json.RawMessage(`1`),
		Jsonrpc: "2.0",
		Method:  "eth_subscribe",
		Params:  json.RawMessage(`["logs"]`),
	}, true, "eth")

	frame, err := wsProtocol.RequestFrame(request)

	require.NoError(t, err)
	require.NotNil(t, frame)
	assert.Equal(t, "logs", frame.SubType)
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var parsed map[string]any
	require.NoError(t, sonic.Unmarshal(body, &parsed))

	return parsed
}

func loadMethodSpecs(t *testing.T) {
	t.Helper()

	specs_utils.LoadMethodSpecs()
}
