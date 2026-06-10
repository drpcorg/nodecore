package flow_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNotNullRequestProcessorStopsAfterFirstNonNull(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Maybe()

	result := []byte(`{"hash":"0xabc"}`)
	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, result, unary.Response.ResponseResult())
	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connector1.AssertExpectations(t)
	connector2.AssertNotCalled(t, "SendRequest", mock.Anything, request)
}

func TestNotNullRequestProcessorReturnsStreamResponse(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Maybe()

	streamResponse := protocol.NewHttpUpstreamResponseStream("1", strings.NewReader(`{"result":"ok"}`), protocol.JsonRpc)
	connector1.On("SendRequest", mock.Anything, request).Return(streamResponse).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.Same(t, streamResponse, unary.Response)
	assert.True(t, unary.Response.HasStream())
	connector2.AssertNotCalled(t, "SendRequest", mock.Anything, request)
}

func TestNotNullRequestProcessorRetriesAfterNull(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Once()

	result := []byte(`{"hash":"0xabc"}`)
	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`null`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, result, unary.Response.ResponseResult())
	upSupervisor.AssertExpectations(t)
	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
}

func TestNotNullRequestProcessorReturnsFirstNullWhenAttemptsExhausted(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Once()

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`null`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`null`), protocol.JsonRpc)).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, []byte(`null`), unary.Response.ResponseResult())
}

func TestNotNullRequestProcessorFailsWhenAllUpstreamsError(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Once()

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewTotalFailureFromErr("1", errors.New("first error"), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewTotalFailureFromErr("1", errors.New("second error"), protocol.JsonRpc)).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.True(t, unary.Response.HasError())
	assert.Contains(t, unary.Response.GetError().Message, "first error")
}

func TestNotNullRequestProcessorPrefersNullOverErrorsWhenNoValueExists(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Once()

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`null`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewTotalFailureFromErr("1", errors.New("second error"), protocol.JsonRpc)).Once()

	processor := flow.NewNotNullRequestProcessor(upSupervisor)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, []byte(`null`), unary.Response.ResponseResult())
}
