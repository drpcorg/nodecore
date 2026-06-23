package flow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func testEthSubscribeRequest() protocol.RequestHolder {
	// Load real specs so eth_subscribe resolves to a Method with the right
	// subscription settings; the test asserts on the production-shaped id.
	_ = specs.NewMethodSpecLoader().Load()
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}
	return protocol.NewUpstreamJsonRpcRequest("223", body, false, "eth")
}

func testEthSubscribeRequestWithId(id string) protocol.RequestHolder {
	_ = specs.NewMethodSpecLoader().Load()
	body := protocol.JsonRpcRequestBody{Id: []byte(id), Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}
	return protocol.NewUpstreamJsonRpcRequest(id, body, false, "eth")
}

func newSubProcessor(upSupervisor *mocks.UpstreamSupervisorMock, subCtx *flow.SubCtx) *flow.SubscriptionRequestProcessor {
	// No local-newHeads availability, so these tests exercise the generic
	// node-backed path; tests that want local synthesis override this.
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(nil).Maybe()
	engine := subengine.NewRegistry(context.Background()).Get(chains.ETHEREUM)
	return flow.NewSubscriptionRequestProcessor(chains.ETHEREUM, upSupervisor, engine, subCtx, nil, allLocalSubs)
}

// allLocalSubs enables every local subscription type (the default).
var allLocalSubs = config.LocalSubSettings{NewHeads: true, Logs: true, PendingTx: true}

func TestSubscriptionRequestProcessorAndCantSelectUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	request := testEthSubscribeRequest()
	err := errors.New("selection error")
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())

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
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(nil, err)

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

// On subscribe the engine seeds a synthetic confirmation, so the first frame a
// client receives is always its subscription-id ack.
func TestSubscriptionRequestProcessorEmitsSubIdAck(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	response := processor.ProcessRequest(context.Background(), strategy, request)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	ackWrapper := <-subRespWrappers

	assert.IsType(t, &protocol.SubscriptionMessageResponse{}, ackWrapper.Response)
	assert.False(t, ackWrapper.Response.HasError())
	assert.Equal(t, "223", ackWrapper.RequestId)
	assert.Equal(t, flow.NoUpstream, ackWrapper.UpstreamId)
}

func TestSubscriptionRequestProcessorAndCancelCtxThenChannelCloses(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	response := processor.ProcessRequest(ctx, strategy, request)
	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers

	cancel()
	// Drain until the response channel is closed; cancellation must terminate
	// the processor goroutine.
	for range subRespWrappers {
	}

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
}

func TestSubscriptionRequestProcessorAndSubscribeThenReceiveEvent(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx := context.Background()
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)
	event := []byte("event")
	go func() {
		respChan <- &protocol.WsResponse{Message: event, SubId: "upstream-sub"}
	}()

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	<-subRespWrappers // subscription-id ack
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.IsType(t, &protocol.SubscriptionMethodResultResponse{}, responseWrapper.Response)
	assert.Equal(t, event, responseWrapper.Response.ResponseResult())
	assert.False(t, responseWrapper.Response.HasError())
	assert.False(t, responseWrapper.Response.HasStream())
	assert.Equal(t, "223", responseWrapper.RequestId)
	assert.Equal(t, "id", responseWrapper.UpstreamId)
}

// Two clients subscribing to the same (method+params) share one upstream
// subscription but each receives its own distinct subscription-id ack, and both
// receive the fanned-out event.
func TestSubscriptionRequestProcessorTwoSubscribersShareOneUpstreamSub(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx := context.Background()

	// No local-newHeads availability → both clients use the generic path.
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(nil).Maybe()
	// One shared engine backs both client processors.
	engine := subengine.NewRegistry(ctx).Get(chains.ETHEREUM)
	p1 := flow.NewSubscriptionRequestProcessor(chains.ETHEREUM, upSupervisor, engine, flow.NewSubCtx(), nil, allLocalSubs)
	p2 := flow.NewSubscriptionRequestProcessor(chains.ETHEREUM, upSupervisor, engine, flow.NewSubCtx(), nil, allLocalSubs)

	req1 := testEthSubscribeRequestWithId("c1")
	req2 := testEthSubscribeRequestWithId("c2")
	respChan := make(chan *protocol.WsResponse, 4)

	// The upstream subscription must be built exactly once, regardless of the
	// number of clients.
	strategy.On("SelectUpstream", mock.Anything).Return("id", nil).Once()
	upSupervisor.On("GetUpstream", "id").Return(upstream).Once()
	apiConnector.On("Subscribe", mock.Anything, mock.Anything).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil).Once()
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil).Once()
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	// First client subscribes and builds the shared source.
	resp1 := p1.ProcessRequest(ctx, strategy, req1).(*flow.SubscriptionResponse).ResponseWrappers
	ack1 := <-resp1
	// Second client reuses the shared source.
	resp2 := p2.ProcessRequest(ctx, strategy, req2).(*flow.SubscriptionResponse).ResponseWrappers
	ack2 := <-resp2

	assert.IsType(t, &protocol.SubscriptionMessageResponse{}, ack1.Response)
	assert.IsType(t, &protocol.SubscriptionMessageResponse{}, ack2.Response)
	subId1 := ack1.Response.ResponseResult()
	subId2 := ack2.Response.ResponseResult()
	assert.NotEmpty(t, subId1)
	assert.NotEmpty(t, subId2)
	assert.NotEqual(t, subId1, subId2, "each client must get its own subscription id")

	// A single upstream event fans out to both clients.
	respChan <- &protocol.WsResponse{SubId: "upstream-sub", Message: []byte("e")}
	event1 := <-resp1
	event2 := <-resp2
	assert.Equal(t, []byte("e"), event1.Response.ResponseResult())
	assert.Equal(t, []byte("e"), event2.Response.ResponseResult())

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	apiConnector.AssertExpectations(t)
}

// When the shared upstream subscription drops (channel closes), the engine
// propagates a terminal error and the processor surfaces it as a total failure
// to the client (no durable reconnect).
func TestSubscriptionRequestProcessorPropagatesUpstreamDisconnect(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx())
	respChan := make(chan *protocol.WsResponse)

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	subRespWrappers := processor.ProcessRequest(context.Background(), strategy, request).(*flow.SubscriptionResponse).ResponseWrappers
	<-subRespWrappers // subscription-id ack

	// Upstream subscription drops.
	close(respChan)

	terminal := <-subRespWrappers
	assert.True(t, terminal.Response.HasError())
	assert.Equal(t, protocol.WsTotalFailureError(), terminal.Response.GetError())
}

func TestSubscriptionRequestProcessorAndSubscribeThenReceiveResultOnlyEvent(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewWsConnectorMock()
	request := testEthSubscribeRequest()
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	ctx := context.Background()
	processor := newSubProcessor(upSupervisor, flow.NewSubCtx().WithSubscriptionResultOnly(true))
	respChan := make(chan *protocol.WsResponse)
	result := []byte(`{"foo":"bar"}`)
	go func() {
		respChan <- &protocol.WsResponse{Message: result, SubId: "upstream-sub"}
	}()

	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("Subscribe", mock.Anything, request).Return(protocol.NewJsonRpcWsUpstreamResponse(respChan, "op-1"), nil)
	apiConnector.On("SubscribeStates", mock.Anything).Return(nil)
	apiConnector.On("Unsubscribe", mock.Anything).Return().Maybe()

	response := processor.ProcessRequest(ctx, strategy, request)

	assert.IsType(t, &flow.SubscriptionResponse{}, response)

	subRespWrappers := response.(*flow.SubscriptionResponse).ResponseWrappers
	<-subRespWrappers // subscription-id ack (skipped by gRPC since it is not an event frame)
	responseWrapper := <-subRespWrappers

	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	subscriptionResponse, ok := responseWrapper.Response.(protocol.SubscriptionResponseHolder)
	assert.True(t, ok)
	assert.True(t, subscriptionResponse.IsEventFrame())
	assert.Equal(t, result, subscriptionResponse.ResponseResult())
	assert.False(t, subscriptionResponse.HasError())
	assert.False(t, subscriptionResponse.HasStream())
	assert.Equal(t, "223", responseWrapper.RequestId)
	assert.Equal(t, "id", responseWrapper.UpstreamId)
}
