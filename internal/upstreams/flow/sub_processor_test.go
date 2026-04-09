package flow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func testEthSubscribeRequest() protocol.RequestHolder {
	specMethod := &specs.Method{
		Name: "eth_subscribe",
		Subscription: &specs.Subscription{
			IsSubscribe: true,
			Method:      "eth_subscription",
			UnsubMethod: "eth_unsubscribe",
		},
	}
	return protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_subscribe", []byte(`["newHeads"]`), false, specMethod)
}

func TestSubscriptionRequestProcessorAndCantSelectUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	request := testEthSubscribeRequest()
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
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	err := errors.New("sub error")
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(nil, err)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)

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
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)

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
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx := context.Background()
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)
	event := []byte("event")
	go func() {
		respChan <- &protocol.WsResponse{Event: event, SubId: "id"}
	}()

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	assert.IsType(t, &protocol.SubscriptionMethodResulResponse{}, responseWrapper.Response)
	assert.Equal(t, []byte(nil), responseWrapper.Response.ResponseResult())
	assert.False(t, responseWrapper.Response.HasError())
	assert.False(t, responseWrapper.Response.HasStream())
	assert.Equal(t, "223", responseWrapper.RequestId)
	assert.Equal(t, "id", responseWrapper.UpstreamId)
}

func TestSubscriptionRequestProcessorAndSubscribeThenReceiveResultOnlyEvent(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx := context.Background()
	processor := flow.NewSubscriptionRequestProcessor(upSupervisor, flow.NewSubCtx().WithSubscriptionResultOnly(true))
	respChan := make(chan *protocol.WsResponse)
	event := []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"id","result":{"foo":"bar"}}}`)
	result := []byte(`{"foo":"bar"}`)
	go func() {
		respChan <- &protocol.WsResponse{Event: event, Message: result, SubId: "id"}
	}()

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	subscriptionResponse, ok := responseWrapper.Response.(protocol.SubscriptionResponseHolder)
	assert.True(t, ok)
	assert.True(t, subscriptionResponse.IsEventFrame())
	assert.Equal(t, result, subscriptionResponse.ResponseResult())
	assert.False(t, subscriptionResponse.HasError())
	assert.False(t, subscriptionResponse.HasStream())
	assert.Equal(t, "223", responseWrapper.RequestId)
	assert.Equal(t, "id", responseWrapper.UpstreamId)
}
