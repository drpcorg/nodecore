package connectors_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestWsConnectorSendUnaryRequestThenReceiveError(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)
	err := errors.New("req error")

	wsProcessor.On("SendRpcRequest", ctx, request).Return(nil, err)

	response := wsConnector.SendRequest(ctx, request)
	expectedError := protocol.ResponseErrorWithData(500, "internal server error: unable to get a response via ws - req error", nil)

	assert.IsType(t, &protocol.ReplyError{}, response)
	assert.True(t, response.HasError())
	assert.False(t, response.HasStream())
	assert.Nil(t, response.ResponseResult())
	assert.Equal(t, "223", response.Id())
	assert.Equal(t, expectedError, response.GetError())
	wsProcessor.AssertExpectations(t)
}

func TestWsConnectorSendUnaryRequestThenResponse(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)
	result := []byte("result")
	wsResponse := &protocol.WsResponse{Message: result}

	wsProcessor.On("SendRpcRequest", ctx, request).Return(wsResponse, nil)

	response := wsConnector.SendRequest(ctx, request)

	assert.IsType(t, &protocol.WsJsonRpcResponse{}, response)
	assert.False(t, response.HasStream())
	assert.False(t, response.HasError())
	assert.Nil(t, response.GetError())
	assert.Equal(t, "223", response.Id())
	assert.Equal(t, result, response.ResponseResult())
	wsProcessor.AssertExpectations(t)
}

func TestWsConnectorType(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)

	assert.Equal(t, protocol.WsConnector, wsConnector.GetType())
}

func TestWsConnectorSendSubThenError(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)
	err := errors.New("sub error")

	wsProcessor.On("SendWsRequest", ctx, request).Return((chan *protocol.WsResponse)(nil), "", err)

	subResp, subErr := wsConnector.Subscribe(ctx, request)

	assert.Nil(t, subResp)
	assert.ErrorIs(t, subErr, err)
	wsProcessor.AssertExpectations(t)
}

func TestWsConnectorSendSubThenResponseChan(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_call", nil, false, nil)
	responseChan := make(chan *protocol.WsResponse)
	wsResponse := &protocol.WsResponse{Message: []byte("result")}
	go func() {
		responseChan <- wsResponse
	}()

	wsProcessor.On("SendWsRequest", ctx, request).Return(responseChan, "op-1", nil)

	subResp, subErr := wsConnector.Subscribe(ctx, request)

	assert.Nil(t, subErr)
	response := <-subResp.ResponseChan()

	assert.Equal(t, response, wsResponse)
	assert.Equal(t, "op-1", subResp.OpId())
	wsProcessor.AssertExpectations(t)
}

func TestWsConnectorSubscribeStates(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	subscriptionName := "upstream_ws_states"
	subscription := utils.NewSubscriptionManager[protocol.SubscribeConnectorState]("test_manager").Subscribe(subscriptionName)

	wsProcessor.On("SubscribeWsStates", subscriptionName).Return(subscription)

	stateSub := wsConnector.SubscribeStates(subscriptionName)

	assert.Same(t, subscription, stateSub)
	wsProcessor.AssertExpectations(t)
}

func TestWsConnectorSubscribeStatesNil(t *testing.T) {
	wsProcessor := mocks.NewWsProcessorMock()
	wsConnector := connectors.NewWsConnector(wsProcessor)
	subscriptionName := "upstream_ws_states"

	wsProcessor.On("SubscribeWsStates", subscriptionName).Return(nil)

	stateSub := wsConnector.SubscribeStates(subscriptionName)

	assert.Nil(t, stateSub)
	wsProcessor.AssertExpectations(t)
}
