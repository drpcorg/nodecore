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

	err := streamNativeCallBody(7, "upstream-1", "erigon/2.60", nil, reader, unwrapJsonRpcResultStream, a.ResultStart, a.Counter, nil, stream)
	require.NoError(t, err)
	// The whole result arrives in one read and ends within it, so end-of-stream
	// is folded into that single data frame - no trailing empty frame.
	require.Len(t, stream.sent, 1)

	assert.Equal(t, "[1,2,3,4]", string(stream.sent[0].GetPayload()))
	assert.True(t, stream.sent[0].GetChunked())
	assert.True(t, stream.sent[0].GetFinalChunk())

	// Response-level metadata is stamped on the (first and only) chunk.
	assert.Equal(t, "erigon/2.60", stream.sent[0].GetUpstreamNodeVersion())
	assert.Equal(t, "upstream-1", stream.sent[0].GetUpstreamId())
}

func TestStreamNativeCallBodyPassesThroughRestBody(t *testing.T) {
	reader := strings.NewReader(`{"hello":"world"}`)
	stream := &testNativeCallStream{ctx: context.Background()}

	err := streamNativeCallBody(7, "upstream-1", "", nil, reader, passThroughStream, -1, protocol.ResultCounter{}, nil, stream)
	require.NoError(t, err)
	// The whole body is forwarded as a single frame plus the terminal empty frame.
	require.Len(t, stream.sent, 2)

	assert.Equal(t, `{"hello":"world"}`, string(stream.sent[0].GetPayload()))
	assert.True(t, stream.sent[0].GetChunked())
	assert.False(t, stream.sent[0].GetFinalChunk())

	assert.Empty(t, stream.sent[1].GetPayload())
	assert.True(t, stream.sent[1].GetChunked())
	assert.True(t, stream.sent[1].GetFinalChunk())
}

func TestStreamNativeCallBodyEmitsTerminalEmptyFinalChunkFallback(t *testing.T) {
	// A scalar (number) result whose terminating '}' lands at the very start of
	// a later read can't be marked final inline: when the result ends, the last
	// digits have already been emitted as a non-final chunk and the '}' read
	// produces no data. End-of-stream then falls back to a trailing empty frame.
	//
	// We size the result so the digits exactly fill the prefix read plus one
	// full MaxChunkSize read, leaving the '}' alone in the next read.
	header := `{"jsonrpc":"2.0","id":"1","result":`
	digitsInPrefix := protocol.MaxChunkSize - len(header)
	digits := digitsInPrefix + protocol.MaxChunkSize
	body := header + strings.Repeat("9", digits) + "}"
	reader := strings.NewReader(body)
	stream := &testNativeCallStream{ctx: context.Background()}
	a := protocol.AnalyzeChunk([]byte(body)[:protocol.MaxChunkSize])

	err := streamNativeCallBody(7, "upstream-1", "", nil, reader, unwrapJsonRpcResultStream, a.ResultStart, a.Counter, nil, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 3)

	// Two non-final data frames carry all the digits...
	assert.False(t, stream.sent[0].GetFinalChunk())
	assert.False(t, stream.sent[1].GetFinalChunk())
	assert.Equal(t, digits, len(stream.sent[0].GetPayload())+len(stream.sent[1].GetPayload()))
	// ...then the terminal empty frame marks the end.
	assert.Empty(t, stream.sent[2].GetPayload())
	assert.True(t, stream.sent[2].GetFinalChunk())
}

type emitterFrame struct {
	payload string
	first   bool
	final   bool
}

func collectingEmitter(got *[]emitterFrame) *nativeCallChunkEmitter {
	return newNativeCallChunkEmitter(func(b []byte, first, final bool) error {
		*got = append(*got, emitterFrame{string(b), first, final})
		return nil
	})
}

func TestNativeCallChunkEmitterForwardsEachWriteImmediately(t *testing.T) {
	var got []emitterFrame
	e := collectingEmitter(&got)

	// Each Write is forwarded as its own frame the moment it arrives - no
	// re-framing, no hold-back.
	n, err := e.Write([]byte("0123"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Len(t, got, 1)
	assert.Equal(t, "0123", got[0].payload)
	assert.True(t, got[0].first)
	assert.False(t, got[0].final)

	_, err = e.Write([]byte("45"))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "45", got[1].payload)
	assert.False(t, got[1].first, "first is true only on the very first chunk")
	assert.False(t, got[1].final)

	// Finish sends the terminal empty final frame.
	require.NoError(t, e.Finish())
	require.Len(t, got, 3)
	assert.Empty(t, got[2].payload)
	assert.True(t, got[2].final)
}

func TestNativeCallChunkEmitterForwardsLargeWriteWhole(t *testing.T) {
	var got []emitterFrame
	e := collectingEmitter(&got)

	// A large Write is forwarded whole - the emitter never splits.
	big := strings.Repeat("x", 100_000)
	_, err := e.Write([]byte(big))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, big, got[0].payload)
	assert.True(t, got[0].first)

	require.NoError(t, e.Finish())
	require.Len(t, got, 2)
	assert.Empty(t, got[1].payload)
	assert.True(t, got[1].final)
}

func TestNativeCallChunkEmitterSkipsEmptyWrites(t *testing.T) {
	var got []emitterFrame
	e := collectingEmitter(&got)

	n, err := e.Write(nil)
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.Empty(t, got, "an empty write must not produce a frame")
}

func TestNativeCallChunkEmitterMarksFinalInline(t *testing.T) {
	var got []emitterFrame
	e := collectingEmitter(&got)

	// A non-final chunk followed by a final one - the final flag rides the last
	// data frame, no trailing empty frame.
	require.NoError(t, e.WriteChunk([]byte("ab"), false))
	require.NoError(t, e.WriteChunk([]byte("cd"), true))
	require.Len(t, got, 2)
	assert.Equal(t, "ab", got[0].payload)
	assert.True(t, got[0].first)
	assert.False(t, got[0].final)
	assert.Equal(t, "cd", got[1].payload)
	assert.False(t, got[1].first)
	assert.True(t, got[1].final)

	// Finish is a no-op once a chunk was already marked final.
	require.NoError(t, e.Finish())
	require.Len(t, got, 2)
}

func TestNativeCallChunkEmitterSingleFinalFrameCarriesFirst(t *testing.T) {
	var got []emitterFrame
	e := collectingEmitter(&got)

	// A response that fits in one chunk: the lone frame is both first and final.
	require.NoError(t, e.WriteChunk([]byte("only"), true))
	require.NoError(t, e.Finish())
	require.Len(t, got, 1)
	assert.Equal(t, "only", got[0].payload)
	assert.True(t, got[0].first)
	assert.True(t, got[0].final)
}

func TestNativeCallSuccessItemsChunking(t *testing.T) {
	items := nativeCallSuccessItems(7, "upstream-1", []byte("0123456789"), 4, nil)
	require.Len(t, items, 3)

	assert.True(t, items[0].GetChunked())
	assert.False(t, items[0].GetFinalChunk())
	assert.Equal(t, "0123", string(items[0].GetPayload()))
	assert.Equal(t, "upstream-1", items[0].GetUpstreamId())

	assert.True(t, items[1].GetChunked())
	assert.False(t, items[1].GetFinalChunk())
	assert.Equal(t, "4567", string(items[1].GetPayload()))
	assert.Empty(t, items[1].GetUpstreamId(), "metadata is stamped on the first chunk only")

	assert.True(t, items[2].GetChunked())
	assert.True(t, items[2].GetFinalChunk())
	assert.Equal(t, "89", string(items[2].GetPayload()))
	assert.Empty(t, items[2].GetUpstreamId(), "metadata is stamped on the first chunk only")
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
