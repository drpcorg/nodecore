package flow_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestUnaryRequestProcessorSubMethodThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.ALEPHZERO
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_subscribe", nil, false, nil)

	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	upSupervisor.AssertNotCalled(t, "GetExecutor")
	upSupervisor.AssertNotCalled(t, "GetUpstream")
	strategy.AssertNotCalled(t, "SelectUpstream")
	cacheProcessor.AssertNotCalled(t, "Store")
	cacheProcessor.AssertNotCalled(t, "Receive")

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(400, "client error - unable to process a subscription request eth_subscribe", nil), unaryRespWrapper.Response.GetError())
}

func TestUnaryRequestProcessorReceiveFromCache(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	result := []byte("result")
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)

	cacheProcessor.On("Receive", ctx, chain, request).Return(result, true)

	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	time.Sleep(10 * time.Millisecond)

	upSupervisor.AssertNotCalled(t, "GetExecutor")
	upSupervisor.AssertNotCalled(t, "GetUpstream")
	strategy.AssertNotCalled(t, "SelectUpstream")
	cacheProcessor.AssertNotCalled(t, "Store")
	cacheProcessor.AssertExpectations(t)

	assert.Equal(t, protocol.Cached, request.RequestObserver().GetRequestKind())
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.False(t, unaryRespWrapper.Response.HasStream())
	assert.Equal(t, result, unaryRespWrapper.Response.ResponseResult())
}

func TestUnaryRequestProcessor_QuorumSkipsCacheReadAndStore(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.POLYGON
	err := errors.New("select err")
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)

	// No cacheProcessor.On("Receive", ...) / ("Store", ...) — if the processor
	// touches the cache when quorum is requested, the mock will fail.
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("", err)

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)

	time.Sleep(10 * time.Millisecond)

	cacheProcessor.AssertNotCalled(t, "Receive")
	cacheProcessor.AssertNotCalled(t, "Store")

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
}

func TestUnaryRequestProcessorCantGetUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	err := errors.New("selection error")
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("", err)

	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	time.Sleep(10 * time.Millisecond)

	upSupervisor.AssertNotCalled(t, "GetUpstream")
	cacheProcessor.AssertNotCalled(t, "Store")
	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(500, "internal server error: selection error", nil), unaryRespWrapper.Response.GetError())
}

func TestUnaryRequestProcessorNoConnectorThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	apiConnector := mocks.NewConnectorMockWithType(protocol.RestConnector)
	chain := chains.POLYGON
	ctx := context.Background()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)

	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper
	time.Sleep(10 * time.Millisecond)

	cacheProcessor.AssertNotCalled(t, "Store")
	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	cacheProcessor.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(500, "internal server error: unable to process a json-rpc request", nil), unaryRespWrapper.Response.GetError())
}

func TestUnaryRequestProcessorReceiveResponseThenStoreInCache(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	apiConnector := mocks.NewConnectorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)
	result := []byte("result")
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	cacheProcessor.On("Store", ctx, chain, request, result).Return()
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", ctx, request).Return(responseHolder)

	processor := flow.NewUnaryRequestProcessor(chain, cacheProcessor, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

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

func upConfig() *config.Upstream {
	return &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}
}

type RequestProcessorMock struct {
	mock.Mock
}

func (r *RequestProcessorMock) ProcessRequest(ctx context.Context, upstreamStrategy flow.UpstreamStrategy, request protocol.RequestHolder) flow.ProcessedResponse {
	args := r.Called(ctx, upstreamStrategy, request)
	return args.Get(0).(flow.ProcessedResponse)
}

func NewRequestProcessorMock() *RequestProcessorMock {
	return &RequestProcessorMock{}
}
