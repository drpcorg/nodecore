package emerald

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
	specs_utils.LoadMethodSpecs()

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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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
	assert.IsType(t, restNativeCallAdapter{}, adapters[requests[0].Id()].adapter)
	// Method is the canonical name as sent by the gRPC client; query
	// params live on RequestParams now, not baked into the path.
	assert.Equal(t, "GET#/v1/blocks/123", requests[0].Method())
	restReq := requests[0].(*protocol.UpstreamRestRequest)
	assert.Equal(t, []string{"true"}, restReq.RequestParams().QueryParams["verbose"])

	assert.Equal(t, protocol.JsonRpc, requests[1].RequestType())
	assert.IsType(t, jsonRpcNativeCallAdapter{}, adapters[requests[1].Id()].adapter)
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}}, requests[1].Selectors())
}

func TestBuildNativeCallRequestsRejectsMalformedJsonRpcPayload(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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

	err := streamNativeCallBody(stream, reader, unwrapJsonRpcResultStream, protocol.JsonRpcResultStreamHint{ResultStart: a.ResultStart, Counter: a.Counter}, replyMeta{requestID: 7, upstreamID: "upstream-1", upstreamNodeVersion: "erigon/2.60"})
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

	err := streamNativeCallBody(stream, reader, passThroughStream, protocol.NoJsonRpcResultStreamHint, replyMeta{requestID: 7, upstreamID: "upstream-1"})
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
	// full continuation read (streamReadChunkSize), leaving the '}' alone in the
	// next read.
	header := `{"jsonrpc":"2.0","id":"1","result":`
	digitsInPrefix := protocol.MaxChunkSize - len(header)
	digits := digitsInPrefix + streamReadChunkSize
	body := header + strings.Repeat("9", digits) + "}"
	reader := strings.NewReader(body)
	stream := &testNativeCallStream{ctx: context.Background()}
	a := protocol.AnalyzeChunk([]byte(body)[:protocol.MaxChunkSize])

	err := streamNativeCallBody(stream, reader, unwrapJsonRpcResultStream, protocol.JsonRpcResultStreamHint{ResultStart: a.ResultStart, Counter: a.Counter}, replyMeta{requestID: 7, upstreamID: "upstream-1"})
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

// dshackle never slices a buffered payload: chunk_size only selects a stream
// request. A buffered response is one unchunked item however large.
func TestSendReplySendsBufferedPayloadAsSingleUnchunkedItem(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 10_000)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", payload, protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner())
	require.NoError(t, err)

	require.Len(t, stream.sent, 1)
	assert.False(t, stream.sent[0].GetChunked())
	assert.Equal(t, payload, stream.sent[0].GetPayload())
	assert.Equal(t, "upstream-1", stream.sent[0].GetUpstreamId())
}

func TestNativeCallSendReplyReturnsSuccessItemWithResponseUpstreamId(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId:          "upstream-1",
		RequestId:           "1",
		Response:            protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
		UpstreamNodeVersion: "erigon/v3.0.0",
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	err := jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner())
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

	err := jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner())
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

	err := restNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)), signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)), signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
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

func flattenKeyValues(items []*dshackle.KeyValue) map[string][]string {
	out := make(map[string][]string, len(items))
	for _, kv := range items {
		out[kv.GetKey()] = append(out[kv.GetKey()], kv.GetValue())
	}
	return out
}

func TestSendReplyForwardsTrailersOnSuccess(t *testing.T) {
	resp := protocol.NewGrpcUpstreamResponse("7", []byte{0x0a, 0x01, 0x41}).
		WithResponseHeaders(http.Header{"content-type": {"application/grpc"}}).
		WithResponseTrailers(map[string][]string{"x-ratelimit-remaining": {"99"}})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "7", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, restNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)

	assert.Equal(t, []string{"application/grpc"}, flattenKeyValues(stream.sent[0].GetResponseHeaders())["content-type"])
	assert.Equal(t, []string{"99"}, flattenKeyValues(stream.sent[0].GetResponseTrailers())["x-ratelimit-remaining"])
}

// A retryable upstream error surfaces as *ReplyError, which carries metadata
// too (RESOURCE_EXHAUSTED with rate-limit hints). Both must reach the client.
func TestSendReplyForwardsHeadersAndTrailersOnReplyError(t *testing.T) {
	request := protocol.NewUpstreamGrpcRequest("7", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	resp := protocol.NewGrpcUpstreamErrorResponse(request, &protocol.GrpcStatus{Code: codes.ResourceExhausted, Message: "slow down"}).(*protocol.ReplyError).
		WithResponseHeaders(http.Header{"x-upstream": {"a"}}).
		WithResponseTrailers(map[string][]string{"retry-after-ms": {"250"}})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "7", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, restNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)

	assert.False(t, stream.sent[0].GetSucceed())
	assert.Equal(t, []string{"a"}, flattenKeyValues(stream.sent[0].GetResponseHeaders())["x-upstream"])
	assert.Equal(t, []string{"250"}, flattenKeyValues(stream.sent[0].GetResponseTrailers())["retry-after-ms"])
}

func TestSendReplyLeavesTrailersEmptyForJsonRpc(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.Empty(t, stream.sent[0].GetResponseTrailers())
}

func grpcItem(id uint32, method string, payload []byte, md ...*dshackle.KeyValue) *dshackle.NativeCallItem {
	return &dshackle.NativeCallItem{
		Id:     id,
		Method: method,
		Data:   &dshackle.NativeCallItem_GrpcData{GrpcData: &dshackle.GrpcData{Payload: payload, Metadata: md}},
	}
}

func TestBuildNativeCallRequestsBuildsGrpcRequest(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	configuredChain := &chains.ConfiguredChain{MethodSpec: "sui"}
	request := &dshackle.NativeCallRequest{
		ChunkSize: 1024, // ignored for gRPC: a unary reply is one message
		Items: []*dshackle.NativeCallItem{
			grpcItem(5, "/sui.rpc.v2.LedgerService/GetObject", []byte{0x0a, 0x02, 0x68, 0x69},
				&dshackle.KeyValue{Key: "x-client", Value: "a"},
				&dshackle.KeyValue{Key: "x-nodecore-key", Value: "secret"},
				&dshackle.KeyValue{Key: "authorization", Value: "Bearer t"}),
		},
	}

	requests, items, failures := service.buildNativeCallRequests(configuredChain, request)
	require.Empty(t, failures)
	require.Len(t, requests, 1)
	assert.IsType(t, grpcNativeCallAdapter{}, items[requests[0].Id()].adapter)

	grpcReq, ok := requests[0].(*protocol.UpstreamGrpcRequest)
	require.True(t, ok)
	assert.Equal(t, protocol.Grpc, grpcReq.RequestType())
	assert.Equal(t, "5", grpcReq.Id())
	assert.Equal(t, "/sui.rpc.v2.LedgerService/GetObject", grpcReq.Method())
	body, err := grpcReq.Body()
	require.NoError(t, err)
	assert.Equal(t, []byte{0x0a, 0x02, 0x68, 0x69}, body)
	assert.False(t, grpcReq.IsStream())
	// reserved credential metadata never reaches an upstream
	assert.Equal(t, map[string][]string{"x-client": {"a"}}, grpcReq.RequestParams().Headers)
}

func TestBuildNativeCallRequestsGrpcUnknownMethodIsUnimplemented(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	request := &dshackle.NativeCallRequest{Items: []*dshackle.NativeCallItem{grpcItem(3, "/sui.rpc.v2.LedgerService/Nope", nil)}}

	requests, _, failures := service.buildNativeCallRequests(&chains.ConfiguredChain{MethodSpec: "sui"}, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, uint32(3), failures[0].GetId())
	assert.False(t, failures[0].GetSucceed())
	assert.Equal(t, int32(codes.Unimplemented), failures[0].GetItemErrorCode())
	assert.Contains(t, failures[0].GetErrorMessage(), "/sui.rpc.v2.LedgerService/Nope")
}

func TestBuildNativeCallRequestsGrpcServerStreamMethodIsRejected(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	request := &dshackle.NativeCallRequest{Items: []*dshackle.NativeCallItem{grpcItem(4, "/sui.rpc.v2.LedgerService/ListTransactions", nil)}}

	requests, _, failures := service.buildNativeCallRequests(&chains.ConfiguredChain{MethodSpec: "sui"}, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, int32(codes.InvalidArgument), failures[0].GetItemErrorCode())
	assert.Contains(t, failures[0].GetErrorMessage(), "NativeSubscribe")
}

func TestBuildNativeCallRequestsGrpcMissingDataIsInvalidArgument(t *testing.T) {
	// an empty GrpcData is a valid empty request message; only a nil oneof
	// branch can be "missing", which adapterFor never routes here - so the
	// guard is exercised directly
	_, failure := grpcNativeCallAdapter{}.BuildRequest(&chains.ConfiguredChain{MethodSpec: "sui"},
		&dshackle.NativeCallItem{Id: 9, Method: "/sui.rpc.v2.LedgerService/GetObject"}, nil, 0)
	require.NotNil(t, failure)
	assert.Equal(t, int32(codes.InvalidArgument), failure.GetItemErrorCode())
}

func TestBuildNativeCallRequestsGrpcSigningUnavailableIsInternal(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	item := grpcItem(6, "/sui.rpc.v2.LedgerService/GetObject", nil)
	item.Nonce = 42
	request := &dshackle.NativeCallRequest{Items: []*dshackle.NativeCallItem{item}}

	requests, _, failures := service.buildNativeCallRequests(&chains.ConfiguredChain{MethodSpec: "sui"}, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, int32(codes.Internal), failures[0].GetItemErrorCode())
}

func TestGrpcSendReplySuccessIsOneUnchunkedItemWithMetadata(t *testing.T) {
	message := bytes.Repeat([]byte{0x0a}, 5_000)
	resp := protocol.NewGrpcUpstreamResponse("5", message).
		WithResponseHeaders(http.Header{"content-type": {"application/grpc"}}).
		WithResponseTrailers(map[string][]string{"x-ratelimit-remaining": {"99"}})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "5", Response: resp, UpstreamNodeVersion: "sui-node/1.50.0"}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, grpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)

	item := stream.sent[0]
	assert.True(t, item.GetSucceed())
	assert.False(t, item.GetChunked())
	assert.Equal(t, message, item.GetPayload())
	assert.Equal(t, uint32(5), item.GetId())
	assert.Equal(t, "sui-1", item.GetUpstreamId())
	assert.Equal(t, "sui-node/1.50.0", item.GetUpstreamNodeVersion())
	assert.Equal(t, []string{"application/grpc"}, flattenKeyValues(item.GetResponseHeaders())["content-type"])
	assert.Equal(t, []string{"99"}, flattenKeyValues(item.GetResponseTrailers())["x-ratelimit-remaining"])
}

func TestGrpcSendReplyUpstreamStatusWithDetailsRidesVerbatim(t *testing.T) {
	upstreamStatus, err := status.New(codes.NotFound, "object not found").
		WithDetails(&errdetails.ErrorInfo{Reason: "OBJECT_PRUNED", Domain: "sui.io"})
	require.NoError(t, err)
	statusProto, err := proto.Marshal(upstreamStatus.Proto())
	require.NoError(t, err)

	request := protocol.NewUpstreamGrpcRequest("5", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	resp := protocol.NewGrpcUpstreamErrorResponse(request, &protocol.GrpcStatus{Code: codes.NotFound, Message: "object not found", StatusProto: statusProto})
	resp.(*protocol.GenericUpstreamResponse).WithResponseTrailers(map[string][]string{"x-trace": {"abc"}})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "5", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, grpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)

	item := stream.sent[0]
	assert.False(t, item.GetSucceed())
	assert.Equal(t, int32(codes.NotFound), item.GetItemErrorCode())
	assert.Equal(t, "object not found", item.GetErrorMessage())
	assert.Empty(t, item.GetErrorData())
	assert.Equal(t, []string{"abc"}, flattenKeyValues(item.GetResponseTrailers())["x-trace"])

	var decoded spb.Status
	require.NoError(t, proto.Unmarshal(item.GetErrorAsIs(), &decoded))
	replayed := status.FromProto(&decoded)
	assert.Equal(t, codes.NotFound, replayed.Code())
	require.Len(t, replayed.Details(), 1)
	assert.Equal(t, "OBJECT_PRUNED", replayed.Details()[0].(*errdetails.ErrorInfo).Reason)
}

func TestGrpcSendReplyUpstreamStatusWithoutDetailsHasNoErrorAsIs(t *testing.T) {
	request := protocol.NewUpstreamGrpcRequest("5", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	resp := protocol.NewGrpcUpstreamErrorResponse(request, &protocol.GrpcStatus{Code: codes.InvalidArgument, Message: "bad digest"})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "5", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, grpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.Equal(t, int32(codes.InvalidArgument), stream.sent[0].GetItemErrorCode())
	assert.Equal(t, "bad digest", stream.sent[0].GetErrorMessage())
	assert.Empty(t, stream.sent[0].GetErrorAsIs())
	assert.Empty(t, stream.sent[0].GetErrorData())
}

func TestGrpcSendReplyNodecoreErrorIsMappedToCanonicalCode(t *testing.T) {
	request := protocol.NewUpstreamGrpcRequest("5", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	resp := protocol.NewTotalFailure(request, protocol.NoAvailableUpstreamsError())
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: flow.NoUpstream, RequestId: "5", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, grpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.Equal(t, int32(codes.Unavailable), stream.sent[0].GetItemErrorCode())
	assert.Equal(t, protocol.NoAvailableUpstreamsError().Message, stream.sent[0].GetErrorMessage())
	assert.Empty(t, stream.sent[0].GetErrorAsIs())
}

// JSON-RPC items must keep their current error vocabulary (nodecore codes,
// error_data) - the gRPC renderer applies to gRPC items only.
func TestJsonRpcSendReplyKeepsNodecoreErrorCodes(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  "1",
		Response:   protocol.NewHttpUpstreamResponseWithError(protocol.NoAvailableUpstreamsError()),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.Equal(t, int32(protocol.NoAvailableUpstreams), stream.sent[0].GetItemErrorCode())
}

// A grpc_data item naming a method the chain serves over JSON-RPC must be
// rejected at the edge: routed on, its proto bytes would reach the HTTP
// connector, which cannot build a response of type Grpc.
func TestBuildNativeCallRequestsGrpcItemForJsonRpcMethodIsUnimplemented(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	request := &dshackle.NativeCallRequest{Items: []*dshackle.NativeCallItem{grpcItem(8, "eth_chainId", []byte{0x0a})}}

	requests, _, failures := service.buildNativeCallRequests(&chains.ConfiguredChain{MethodSpec: "eth"}, request)
	require.Empty(t, requests)
	require.Len(t, failures, 1)
	assert.Equal(t, int32(codes.Unimplemented), failures[0].GetItemErrorCode())
	assert.Contains(t, failures[0].GetErrorMessage(), "eth_chainId")
}

// A streamed JSON-RPC response without an unwrap hint must surface as an
// error item, never as a truncated success.
func TestSendReplyStreamWithoutHintIsAnError(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponseStream("1", strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":[1,2,3]}`), protocol.JsonRpc)
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "upstream-1", RequestId: "1", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.False(t, stream.sent[0].GetSucceed())
	assert.Contains(t, stream.sent[0].GetErrorMessage(), "result field is missing")
}

// A request-level failure (here: no upstream supervisor) must answer every
// item under its own id and in its own kind's error vocabulary - a gRPC item
// with a canonical code, a JSON-RPC item with a nodecore code.
func TestNativeCallRequestFailureIsRenderedPerItem(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	stream := &testNativeCallStream{ctx: context.Background()}

	err := service.NativeCall(&dshackle.NativeCallRequest{
		Chain: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET,
		Items: []*dshackle.NativeCallItem{
			grpcItem(3, "/sui.rpc.v2.LedgerService/GetObject", nil),
			{Id: 7, Method: "eth_chainId", Data: &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)}},
		},
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 2)

	grpcFailure, jsonRpcFailure := stream.sent[0], stream.sent[1]
	assert.Equal(t, uint32(3), grpcFailure.GetId())
	assert.Equal(t, int32(codes.Unavailable), grpcFailure.GetItemErrorCode())
	assert.Equal(t, uint32(7), jsonRpcFailure.GetId())
	assert.Equal(t, int32(protocol.NoAvailableUpstreams), jsonRpcFailure.GetItemErrorCode())
	for _, failure := range stream.sent {
		assert.False(t, failure.GetSucceed())
		assert.Equal(t, flow.NoUpstream, failure.GetUpstreamId())
	}
}

// StatusProto bytes that do not unmarshal must not reach the wire: the
// contract tells the client to prefer status.FromProto(error_as_is).
func TestGrpcSendReplyDropsUnparseableStatusProto(t *testing.T) {
	request := protocol.NewUpstreamGrpcRequest("5", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	resp := protocol.NewGrpcUpstreamErrorResponse(request, &protocol.GrpcStatus{
		Code:        codes.NotFound,
		Message:     "object not found",
		StatusProto: []byte{0xff}, // invalid wire tag
	})
	wrapper := &protocol.ResponseHolderWrapper{UpstreamId: "sui-1", RequestId: "5", Response: resp}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, grpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)
	assert.Empty(t, stream.sent[0].GetErrorAsIs())
	assert.Equal(t, int32(codes.NotFound), stream.sent[0].GetItemErrorCode())
	assert.Equal(t, "object not found", stream.sent[0].GetErrorMessage())
}
