package flow_test

import (
	"context"
	"errors"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/flow"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

func TestSubscriptionRequestProcessorAndCantSelectUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	err := errors.New("selection error")
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())

	strategy.On("SelectUpstream", request).Return("", err)

	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	errorWrapper := <-subRespWrappers

	upSupervisor.AssertNotCalled(t, "GetUpstream")
	strategy.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, errorWrapper.UpstreamId)
	assert.Equal(t, "223", errorWrapper.RequestId)
	assert.True(t, errorWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(500, "internal server error: selection error", nil), errorWrapper.Response.GetError())
}

func TestSubscriptionRequestProcessorAndCantSubscribeThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	err := errors.New("sub error")
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(nil, err)

	processor.ProcessRequest(context.Background(), strategy, request)

	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	errorWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, errorWrapper.UpstreamId)
	assert.Equal(t, "223", errorWrapper.RequestId)
	assert.True(t, errorWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(500, "internal server error: sub error", nil), errorWrapper.Response.GetError())
}

func TestSubscriptionRequestProcessorAndCancelCtxThenNothing(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	ctx, cancel := context.WithCancel(context.Background())
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan), nil)

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	cancel()
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	assert.Nil(t, responseWrapper)
}

func TestSubscriptionRequestProcessorAndSubscribeThenReceiveEvent(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	ctx := context.Background()
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)
	event := []byte("event")
	go func() {
		respChan <- &protocol.WsResponse{Event: event}
	}()

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan), nil)

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	assert.IsType(t, &protocol.SubscriptionEventResponse{}, responseWrapper.Response)
	assert.Equal(t, event, responseWrapper.Response.ResponseResult())
	assert.False(t, responseWrapper.Response.HasError())
	assert.False(t, responseWrapper.Response.HasStream())
	assert.Equal(t, "223", responseWrapper.RequestId)
	assert.Equal(t, "id", responseWrapper.UpstreamId)
}

func TestUnaryRequestProcessorSubMethodThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs")
	err := specs.Load()
	assert.NoError(t, err)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.POLYGON
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_subscribe", nil, false)

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
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)

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

	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.False(t, unaryRespWrapper.Response.HasStream())
	assert.Equal(t, result, unaryRespWrapper.Response.ResponseResult())
}

func TestUnaryRequestProcessorCantGetUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	cacheProcessor := mocks.NewCacheProcessorMock()
	chain := chains.POLYGON
	ctx := context.Background()
	err := errors.New("selection error")
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	upSupervisor.On("GetExecutor").Return(createExecutor())
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
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	upSupervisor.On("GetExecutor").Return(createExecutor())
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
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	result := []byte("result")
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)

	cacheProcessor.On("Receive", ctx, chain, request).Return([]byte{}, false)
	cacheProcessor.On("Store", ctx, chain, request, result).Return()
	upSupervisor.On("GetExecutor").Return(createExecutor())
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

	assert.Equal(t, "id", unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, result, unaryRespWrapper.Response.ResponseResult())
}

func createExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return protocol.CreateFlowExecutor()
}

func upConfig() *config.Upstream {
	return &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
	}
}
