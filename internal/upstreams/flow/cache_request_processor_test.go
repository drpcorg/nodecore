package flow_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestCacheRequestProcessorReceiveFromCacheSkipsDelegate(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	delegate := NewRequestProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	result := []byte("result")
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")

	cacheProcessor.On("Receive", ctx, chain, request).Return(result, true)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, delegate)
	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)
	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	time.Sleep(10 * time.Millisecond)

	// a cache hit must short-circuit the delegate entirely
	delegate.AssertNotCalled(t, "ProcessRequest")
	cacheProcessor.AssertNotCalled(t, "Store")
	cacheProcessor.AssertExpectations(t)

	assert.Equal(t, protocol.Cached, request.RequestObserver().GetRequestKind())
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.False(t, unaryRespWrapper.Response.HasStream())
	assert.Equal(t, result, unaryRespWrapper.Response.ResponseResult())
}

func TestCacheRequestProcessorQuorumSkipsCacheAndDelegates(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	delegate := NewRequestProcessorMock()
	chain := chains.POLYGON
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")
	delegateResponse := &flow.UnaryResponse{ResponseWrapper: &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  "223",
		Response:   protocol.NewSimpleHttpUpstreamResponse("223", []byte("result"), protocol.JsonRpc),
	}}

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	// No cacheProcessor.On(...) — touching the cache under quorum fails the mock.
	delegate.On("ProcessRequest", ctx, strategy, request).Return(delegateResponse)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, delegate)
	response := processor.ProcessRequest(ctx, strategy, request)

	time.Sleep(10 * time.Millisecond)

	cacheProcessor.AssertNotCalled(t, "Receive")
	cacheProcessor.AssertNotCalled(t, "Store")
	delegate.AssertExpectations(t)
	assert.Same(t, delegateResponse, response)
}

func TestCacheRequestProcessorMissDelegatesAndStores(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	delegate := NewRequestProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	result := []byte("result")
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")
	delegateResponse := &flow.UnaryResponse{ResponseWrapper: &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  "223",
		Response:   protocol.NewSimpleHttpUpstreamResponse("223", result, protocol.JsonRpc),
	}}

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	cacheProcessor.On("Store", ctx, chain, request, result).Return()
	delegate.On("ProcessRequest", ctx, strategy, request).Return(delegateResponse)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, delegate)
	response := processor.ProcessRequest(ctx, strategy, request)

	time.Sleep(10 * time.Millisecond)

	delegate.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)
	assert.Same(t, delegateResponse, response)
}

func TestCacheRequestProcessorErrorResponseNotStored(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	delegate := NewRequestProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")
	delegateResponse := &flow.UnaryResponse{ResponseWrapper: &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  "223",
		Response:   protocol.NewTotalFailureFromErr("223", assert.AnError, request.RequestType()),
	}}

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	delegate.On("ProcessRequest", ctx, strategy, request).Return(delegateResponse)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, delegate)
	response := processor.ProcessRequest(ctx, strategy, request)

	time.Sleep(10 * time.Millisecond)

	cacheProcessor.AssertNotCalled(t, "Store")
	delegate.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)
	assert.Same(t, delegateResponse, response)
}

func TestCacheRequestProcessorSubscriptionResponseNotStored(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	delegate := NewRequestProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")
	delegateResponse := &flow.SubscriptionResponse{ResponseWrappers: make(chan *protocol.ResponseHolderWrapper)}

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	delegate.On("ProcessRequest", ctx, strategy, request).Return(delegateResponse)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, delegate)
	response := processor.ProcessRequest(ctx, strategy, request)

	time.Sleep(10 * time.Millisecond)

	cacheProcessor.AssertNotCalled(t, "Store")
	delegate.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)
	assert.Same(t, delegateResponse, response)
}

// TestCacheRequestProcessorWrappingUnaryStoresFreshResponse exercises the full
// Cache(Unary) path: a cache miss runs the real unary processor against an
// upstream and the fresh response is stored.
func TestCacheRequestProcessorWrappingUnaryStoresFreshResponse(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	apiConnector := mocks.NewConnectorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	_ = specs.NewMethodSpecLoader().Load()
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "eth")
	result := []byte("result")
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	cacheProcessor.On("Store", ctx, chain, request, result).Return()
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", ctx, request).Return(responseHolder)

	processor := flow.NewCacheRequestProcessor(chain, cacheProcessor, flow.NewUnaryRequestProcessor(chain, upSupervisor))
	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)
	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	time.Sleep(10 * time.Millisecond)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	assert.Equal(t, "id", unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, result, unaryRespWrapper.Response.ResponseResult())
}
