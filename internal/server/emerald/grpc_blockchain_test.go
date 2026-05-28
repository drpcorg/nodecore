package emerald

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMapNativeSubscribeMethod(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	t.Run("uses native subscribe method as is", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", []byte(`["newHeads"]`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["newHeads"]`, string(payload))
	})

	t.Run("uses native subscribe method with default empty payload", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", nil)
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `[]`, string(payload))
	})

	t.Run("fails native subscribe method with invalid payload", func(te *testing.T) {
		_, _, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", []byte(`not-json`))
		require.Error(te, err)
		assert.Contains(te, err.Error(), "invalid subscribe payload format")
	})

	t.Run("maps api style method to eth_subscribe", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "newHeads", []byte(`[{"foo":"bar"}]`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["newHeads",{"foo":"bar"}]`, string(payload))
	})

	t.Run("returns unimplemented mapping error for unsupported method", func(te *testing.T) {
		_, _, err := mapNativeSubscribeMethod("solana", nil, "newHeads", nil)
		require.Error(te, err)
		assert.ErrorIs(te, err, errSubscribeMappingNotSupported)
	})
}

func TestBuildNativeCallRequestsRoutesByItemKind(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}

	request := &dshackle.NativeCallRequest{
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "GET#/v1/blocks/123",
				Data: &dshackle.NativeCallItem_RestData{
					RestData: &dshackle.RestData{
						QueryParams: []*dshackle.KeyValue{{Key: "verbose", Value: "true"}},
					},
				},
			},
			{
				Id:     2,
				Method: "eth_chainId",
				Data: &dshackle.NativeCallItem_Payload{
					Payload: []byte(`[]`),
				},
			},
		},
	}

	requests, adapters, failures := service.buildNativeCallRequests(configuredChain, request)
	require.Empty(t, failures)
	require.Len(t, requests, 2)
	require.Len(t, adapters, 2)

	assert.Equal(t, protocol.Rest, requests[0].RequestType())
	assert.IsType(t, restNativeCallAdapter{}, adapters[requests[0].Id()])
	// Method is the canonical name as sent by the gRPC client; query
	// params live on RequestParams now, not baked into the path.
	assert.Equal(t, "GET#/v1/blocks/123", requests[0].Method())
	restReq := requests[0].(*protocol.UpstreamRestRequest)
	assert.Equal(t, []string{"true"}, restReq.RequestParams().QueryParams["verbose"])

	assert.Equal(t, protocol.JsonRpc, requests[1].RequestType())
	assert.IsType(t, jsonRpcNativeCallAdapter{}, adapters[requests[1].Id()])
}

func TestBuildNativeCallRequestsRejectsMalformedJsonRpcPayload(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}

	request := &dshackle.NativeCallRequest{
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "eth_call",
				Data: &dshackle.NativeCallItem_Payload{
					Payload: []byte(`not-json`),
				},
			},
		},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, uint32(1), failures[0].GetId())
	assert.False(t, failures[0].GetSucceed())
	assert.Equal(t, int32(400), failures[0].GetItemErrorCode())
}

func TestBuildNativeCallRequestsRejectsMalformedRestMethod(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "algorand"}

	request := &dshackle.NativeCallRequest{
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "no-verb-separator",
				Data: &dshackle.NativeCallItem_RestData{
					RestData: &dshackle.RestData{},
				},
			},
		},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, uint32(1), failures[0].GetId())
	assert.False(t, failures[0].GetSucceed())
	assert.Equal(t, int32(400), failures[0].GetItemErrorCode())
}

func TestBuildNativeCallRequestsMarksStreamMethods(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}
	request := &dshackle.NativeCallRequest{
		ChunkSize: 100,
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "eth_getLogs",
				Data: &dshackle.NativeCallItem_Payload{
					Payload: []byte(`[]`),
				},
			},
		},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)
	require.Empty(t, failures)
	require.Len(t, requests, 1)
	assert.True(t, requests[0].IsStream())
}

func TestStreamNativeCallBodyUnwrapsJsonRpcResult(t *testing.T) {
	reader := strings.NewReader(`{"jsonrpc":"2.0","id":"1","result":[1,2,3,4]}`)
	stream := &testNativeCallStream{ctx: context.Background()}

	err := streamNativeCallBody(7, "upstream-1", reader, 4, unwrapJsonRpcResultStream, nil, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 3)

	assert.Equal(t, "[1,2", string(stream.sent[0].GetPayload()))
	assert.True(t, stream.sent[0].GetChunked())
	assert.False(t, stream.sent[0].GetFinalChunk())

	assert.Equal(t, ",3,4", string(stream.sent[1].GetPayload()))
	assert.True(t, stream.sent[1].GetChunked())
	assert.False(t, stream.sent[1].GetFinalChunk())

	assert.Equal(t, "]", string(stream.sent[2].GetPayload()))
	assert.True(t, stream.sent[2].GetChunked())
	assert.True(t, stream.sent[2].GetFinalChunk())
}

func TestStreamNativeCallBodyPassesThroughRestBody(t *testing.T) {
	reader := strings.NewReader(`{"hello":"world"}`)
	stream := &testNativeCallStream{ctx: context.Background()}

	err := streamNativeCallBody(7, "upstream-1", reader, 8, passThroughStream, nil, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 3)

	assert.Equal(t, `{"hello"`, string(stream.sent[0].GetPayload()))
	assert.True(t, stream.sent[0].GetChunked())
	assert.False(t, stream.sent[0].GetFinalChunk())

	assert.Equal(t, `:"world"`, string(stream.sent[1].GetPayload()))
	assert.True(t, stream.sent[1].GetChunked())
	assert.False(t, stream.sent[1].GetFinalChunk())

	assert.Equal(t, `}`, string(stream.sent[2].GetPayload()))
	assert.True(t, stream.sent[2].GetChunked())
	assert.True(t, stream.sent[2].GetFinalChunk())
}

func TestNativeCallSuccessItemsChunking(t *testing.T) {
	items := nativeCallSuccessItems(7, "upstream-1", []byte("0123456789"), 4, nil)
	require.Len(t, items, 3)

	assert.True(t, items[0].GetChunked())
	assert.False(t, items[0].GetFinalChunk())
	assert.Equal(t, "0123", string(items[0].GetPayload()))

	assert.True(t, items[1].GetChunked())
	assert.False(t, items[1].GetFinalChunk())
	assert.Equal(t, "4567", string(items[1].GetPayload()))

	assert.True(t, items[2].GetChunked())
	assert.True(t, items[2].GetFinalChunk())
	assert.Equal(t, "89", string(items[2].GetPayload()))
}

// REST GET responses carry meaningful headers (Content-Type, CORS,
// quorum signatures, ...). Streaming + error replies already forwarded
// them; this test pins the unary-success path so a future refactor
// can't silently drop them again.
func TestSendReplyForwardsResponseHeadersOnUnarySuccess(t *testing.T) {
	upstreamHeaders := http.Header{
		"Content-Type":    {"application/json"},
		"X-Custom-Header": {"alpha", "beta"},
	}
	resp := protocol.NewSimpleHttpUpstreamResponse("42", []byte(`{"ok":true}`), protocol.Rest).
		WithResponseHeaders(upstreamHeaders)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "42",
		Response:   resp,
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := restNativeCallAdapter{}.SendReply(stream, wrapper, 0)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)

	got := stream.sent[0].GetResponseHeaders()
	require.NotEmpty(t, got, "unary REST success must forward upstream headers")

	flattened := make(map[string][]string)
	for _, kv := range got {
		flattened[kv.GetKey()] = append(flattened[kv.GetKey()], kv.GetValue())
	}
	assert.Equal(t, []string{"application/json"}, flattened["Content-Type"])
	assert.ElementsMatch(t, []string{"alpha", "beta"}, flattened["X-Custom-Header"],
		"repeated header values must round-trip through the gRPC reply")
}

func TestNativeCallUnauthenticated(t *testing.T) {
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)))
	stream := &testNativeCallStream{ctx: context.Background()}

	err := service.NativeCall(&dshackle.NativeCallRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "no metadata")

	stream.ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("sessionid", "unknown"))
	err = service.NativeCall(&dshackle.NativeCallRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "does not exist")
}

func TestNativeSubscribeUnauthenticated(t *testing.T) {
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)))
	stream := &testNativeSubscribeStream{ctx: context.Background()}

	err := service.NativeSubscribe(&dshackle.NativeSubscribeRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "no metadata")
}

type testNativeCallStream struct {
	ctx  context.Context
	sent []*dshackle.NativeCallReplyItem
}

func (t *testNativeCallStream) Send(item *dshackle.NativeCallReplyItem) error {
	t.sent = append(t.sent, item)
	return nil
}

func (t *testNativeCallStream) SetHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeCallStream) SendHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeCallStream) SetTrailer(_ metadata.MD) {}

func (t *testNativeCallStream) Context() context.Context {
	return t.ctx
}

func (t *testNativeCallStream) SendMsg(_ any) error {
	return nil
}

func (t *testNativeCallStream) RecvMsg(_ any) error {
	return nil
}

type testNativeSubscribeStream struct {
	ctx  context.Context
	sent []*dshackle.NativeSubscribeReplyItem
}

func (t *testNativeSubscribeStream) Send(item *dshackle.NativeSubscribeReplyItem) error {
	t.sent = append(t.sent, item)
	return nil
}

func (t *testNativeSubscribeStream) SetHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeSubscribeStream) SendHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeSubscribeStream) SetTrailer(_ metadata.MD) {}

func (t *testNativeSubscribeStream) Context() context.Context {
	return t.ctx
}

func (t *testNativeSubscribeStream) SendMsg(_ any) error {
	return nil
}

func (t *testNativeSubscribeStream) RecvMsg(_ any) error {
	return nil
}
