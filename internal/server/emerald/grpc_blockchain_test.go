package emerald

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// subMethodsChainSupervisor is a ChainSupervisor stub exposing a fixed
// SubMethods set.
type subMethodsChainSupervisor struct {
	upstreams.ChainSupervisor
	subMethods mapset.Set[string]
}

func (s subMethodsChainSupervisor) GetChainState() upstreams.ChainSupervisorState {
	return upstreams.ChainSupervisorState{SubMethods: s.subMethods}
}

func chainSupWithSubMethods(methods ...string) subMethodsChainSupervisor {
	return subMethodsChainSupervisor{subMethods: mapset.NewThreadUnsafeSet[string](methods...)}
}

func TestSubscribeMethodSupported(t *testing.T) {
	// gRPC validates the requested method against the chain's advertised topics
	assert.True(t, subscribeMethodSupported(chainSupWithSubMethods("newHeads", "logs"), "newHeads"))
	assert.True(t, subscribeMethodSupported(chainSupWithSubMethods("newHeads", "logs"), "logs"))
	// logs not advertised (e.g. no eth_getLogs / no ws head) -> unsupported
	assert.False(t, subscribeMethodSupported(chainSupWithSubMethods("newHeads"), "logs"))
	// nothing advertised (e.g. no ws upstream) -> unsupported
	assert.False(t, subscribeMethodSupported(chainSupWithSubMethods(), "newHeads"))
	// native (non-EVM) sub methods pass through by name
	assert.True(t, subscribeMethodSupported(chainSupWithSubMethods("programSubscribe"), "programSubscribe"))
}

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

	t.Run("maps dshackle null payload to eth_subscribe without extra args", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "newHeads", []byte(`null`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["newHeads"]`, string(payload))
	})

	t.Run("maps dshackle logs object payload to eth_subscribe", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "logs", []byte(`{"address":"0xabc","topics":[]}`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["logs",{"address":"0xabc","topics":[]}]`, string(payload))
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
				Id:        2,
				Method:    "eth_chainId",
				Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us"}}}}},
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
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}}, requests[1].Selectors())
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
	body := `{"jsonrpc":"2.0","id":"1","result":[1,2,3,4]}`
	reader := strings.NewReader(body)
	stream := &testNativeCallStream{ctx: context.Background()}
	a := protocol.AnalyzeChunk([]byte(body))

	err := streamNativeCallBody(7, "upstream-1", "erigon/2.60", nil, reader, 4, unwrapJsonRpcResultStream, a.ResultStart, a.Counter, nil, stream)
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
	assert.Equal(t, "erigon/2.60", stream.sent[0].GetUpstreamNodeVersion())
	assert.Equal(t, "erigon/2.60", stream.sent[1].GetUpstreamNodeVersion())
	assert.Equal(t, "erigon/2.60", stream.sent[2].GetUpstreamNodeVersion())
}

func TestStreamNativeCallBodyPassesThroughRestBody(t *testing.T) {
	reader := strings.NewReader(`{"hello":"world"}`)
	stream := &testNativeCallStream{ctx: context.Background()}

	err := streamNativeCallBody(7, "upstream-1", "", nil, reader, 8, passThroughStream, -1, protocol.ResultCounter{}, nil, stream)
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

func TestStreamNativeCallBodyExactChunkBoundaryEmitsEmptyFinal(t *testing.T) {
	// The unwrapped result "[1,2,3,4]" is 9 bytes; with chunk_size 3 it splits
	// into exactly three full chunks, so the terminal frame carries an empty
	// payload (there is no partial tail to flag final).
	body := `{"jsonrpc":"2.0","id":"1","result":[1,2,3,4]}`
	reader := strings.NewReader(body)
	stream := &testNativeCallStream{ctx: context.Background()}
	a := protocol.AnalyzeChunk([]byte(body))

	err := streamNativeCallBody(7, "upstream-1", "", nil, reader, 3, unwrapJsonRpcResultStream, a.ResultStart, a.Counter, nil, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 4)

	assert.Equal(t, "[1,", string(stream.sent[0].GetPayload()))
	assert.False(t, stream.sent[0].GetFinalChunk())
	assert.Equal(t, "2,3", string(stream.sent[1].GetPayload()))
	assert.False(t, stream.sent[1].GetFinalChunk())
	assert.Equal(t, ",4]", string(stream.sent[2].GetPayload()))
	assert.False(t, stream.sent[2].GetFinalChunk())
	assert.Empty(t, stream.sent[3].GetPayload())
	assert.True(t, stream.sent[3].GetFinalChunk())
}

func TestNativeCallChunkEmitterEmitsWithoutLag(t *testing.T) {
	type frame struct {
		payload string
		final   bool
	}
	var got []frame
	e := newNativeCallChunkEmitter(4, func(b []byte, final bool) error {
		got = append(got, frame{string(b), final})
		return nil
	})

	// A full chunk is emitted the moment it completes - no one-chunk hold-back.
	n, err := e.Write([]byte("0123"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Len(t, got, 1)
	assert.Equal(t, "0123", got[0].payload)
	assert.False(t, got[0].final)

	// The trailing partial chunk only goes out on Finish, flagged final.
	_, err = e.Write([]byte("45"))
	require.NoError(t, err)
	require.Len(t, got, 1, "partial chunk must not be emitted until Finish")
	require.NoError(t, e.Finish())
	require.Len(t, got, 2)
	assert.Equal(t, "45", got[1].payload)
	assert.True(t, got[1].final)
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

func TestNativeCallSendReplyReturnsSuccessItemWithResponseUpstreamId(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId:          "upstream-1",
		RequestId:           "1",
		Response:            protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
		UpstreamNodeVersion: "erigon/v3.0.0",
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)

	assert.Equal(t, "upstream-1", stream.sent[0].GetUpstreamId())
	assert.Equal(t, "erigon/v3.0.0", stream.sent[0].GetUpstreamNodeVersion())
	assert.Equal(t, uint32(1), stream.sent[0].GetId())
	assert.True(t, stream.sent[0].GetSucceed())
	assert.Equal(t, `"0x1"`, string(stream.sent[0].GetPayload()))
}

func TestNativeCallSendReplyReturnsErrorItemWithResponseUpstreamId(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId:          "upstream-err",
		RequestId:           "1",
		Response:            protocol.NewHttpUpstreamResponseWithError(protocol.ServerErrorWithCause(fmt.Errorf("message"))),
		UpstreamNodeVersion: "reth/v1.6.0",
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)

	assert.Equal(t, "upstream-err", stream.sent[0].GetUpstreamId())
	assert.Equal(t, "reth/v1.6.0", stream.sent[0].GetUpstreamNodeVersion())
	assert.Equal(t, uint32(1), stream.sent[0].GetId())
	assert.False(t, stream.sent[0].GetSucceed())
	assert.NotEmpty(t, stream.sent[0].GetErrorMessage())
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
		UpstreamId:          "upstream-1",
		RequestId:           "42",
		Response:            resp,
		UpstreamNodeVersion: "geth/1.14",
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := restNativeCallAdapter{}.SendReply(stream, wrapper, 0)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)

	got := stream.sent[0].GetResponseHeaders()
	assert.Equal(t, "geth/1.14", stream.sent[0].GetUpstreamNodeVersion())
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

func TestBuildNativeCallRequestsPropagatesRequestSelectorToJsonRpcAndRestItems(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}
	request := &dshackle.NativeCallRequest{
		Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us"}}}},
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "eth_chainId",
				Data:   &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)},
			},
			{
				Id:     2,
				Method: "GET#/v1/blocks/123",
				Data: &dshackle.NativeCallItem_RestData{RestData: &dshackle.RestData{
					QueryParams: []*dshackle.KeyValue{{Key: "verbose", Value: "true"}},
				}},
			},
		},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)

	require.Empty(t, failures)
	require.Len(t, requests, 2)
	for _, builtRequest := range requests {
		assert.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}}, builtRequest.Selectors())
	}
}

func TestBuildNativeCallRequestsAppendsRequestAndItemSelectors(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}
	request := &dshackle.NativeCallRequest{
		Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us"}}}},
		Items: []*dshackle.NativeCallItem{{
			Id:        1,
			Method:    "eth_chainId",
			Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_ExistsSelector{ExistsSelector: &dshackle.ExistsSelector{Name: "archive"}}}},
			Data:      &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)},
		}},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)

	require.Empty(t, failures)
	require.Len(t, requests, 1)
	assert.Equal(t, []protocol.RequestSelector{
		protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
		protocol.RequestExistsSelector{Name: "archive"},
	}, requests[0].Selectors())
}

func TestBuildNativeCallRequestsRejectsConflictingRequestAndItemSortSelectorsAtMapping(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}
	request := &dshackle.NativeCallRequest{
		Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_SAFE}}}},
		Items: []*dshackle.NativeCallItem{{
			Id:        1,
			Method:    "eth_chainId",
			Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_FINALIZED}}}}},
			Data:      &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)},
		}},
	}

	requests, _, failures := service.buildNativeCallRequests(configuredChain, request)

	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, uint32(1), failures[0].GetId())
	assert.False(t, failures[0].GetSucceed())
	assert.Equal(t, int32(400), failures[0].GetItemErrorCode())
	assert.Contains(t, failures[0].GetErrorMessage(), "conflicting selector sort hints")
}
