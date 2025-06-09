package connectors_test

import (
	"context"
	"errors"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestWsConnectorSendUnaryRequestThenReceiveError(t *testing.T) {
	connection := mocks.NewWsConnectionMock()
	wsConnector := connectors.NewWsConnector(connection)
	ctx := context.Background()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	err := errors.New("req error")

	connection.On("SendRpcRequest", ctx, request).Return(nil, err)

	response := wsConnector.SendRequest(ctx, request)
	expectedError := protocol.ResponseErrorWithData(500, "internal server error: unable to get a response via ws - req error", nil)

	assert.IsType(t, &protocol.ReplyError{}, response)
	assert.True(t, response.HasError())
	assert.False(t, response.HasStream())
	assert.Nil(t, response.ResponseResult())
	assert.Equal(t, "223", response.Id())
	assert.Equal(t, expectedError, response.GetError())
}

func TestWsConnectorSendUnaryRequestThenResponse(t *testing.T) {
	connection := mocks.NewWsConnectionMock()
	wsConnector := connectors.NewWsConnector(connection)
	ctx := context.Background()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	result := []byte("result")
	wsResponse := &protocol.WsResponse{Message: result}

	connection.On("SendRpcRequest", ctx, request).Return(wsResponse, nil)

	response := wsConnector.SendRequest(ctx, request)

	assert.IsType(t, &protocol.WsJsonRpcResponse{}, response)
	assert.False(t, response.HasStream())
	assert.False(t, response.HasError())
	assert.Nil(t, response.GetError())
	assert.Equal(t, "223", response.Id())
	assert.Equal(t, result, response.ResponseResult())
}

func TestWsConnectorType(t *testing.T) {
	connection := mocks.NewWsConnectionMock()
	wsConnector := connectors.NewWsConnector(connection)

	assert.Equal(t, protocol.WsConnector, wsConnector.GetType())
}

func TestWsConnectorSendSubThenError(t *testing.T) {
	connection := mocks.NewWsConnectionMock()
	wsConnector := connectors.NewWsConnector(connection)
	ctx := context.Background()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	err := errors.New("sub error")

	connection.On("SendWsRequest", ctx, request).Return(nil, err)

	subResp, subErr := wsConnector.Subscribe(ctx, request)

	assert.Nil(t, subResp)
	assert.ErrorIs(t, subErr, err)
}

func TestWsConnectorSendSubThenResponseChan(t *testing.T) {
	connection := mocks.NewWsConnectionMock()
	wsConnector := connectors.NewWsConnector(connection)
	ctx := context.Background()
	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	responseChan := make(chan *protocol.WsResponse)
	wsResponse := &protocol.WsResponse{Message: []byte("result")}
	go func() {
		responseChan <- wsResponse
	}()

	connection.On("SendWsRequest", ctx, request).Return(responseChan, nil)

	subResp, subErr := wsConnector.Subscribe(ctx, request)

	assert.Nil(t, subErr)
	response := <-subResp.ResponseChan()

	assert.Equal(t, response, wsResponse)
}
