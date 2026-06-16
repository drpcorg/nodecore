package flow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFanoutRequestProcessorBroadcastPartialFailure(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "eth")

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

	result := []byte(`"0xtx"`)
	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewTotalFailureFromErr("1", errors.New("nonce too low"), protocol.JsonRpc)).Once()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchBroadcast)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, result, unary.Response.ResponseResult())
	strategy.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
}

func TestFanoutRequestProcessorBroadcastAllErrors(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "eth")

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

	connector1.On("SendRequest", mock.Anything, request).Run(func(args mock.Arguments) {
		time.Sleep(20 * time.Millisecond)
	}).Return(protocol.NewTotalFailureFromErr("1", errors.New("already known"), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewTotalFailureFromErr("1", errors.New("nonce too low"), protocol.JsonRpc)).Once()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchBroadcast)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.True(t, unary.Response.HasError())
	assert.Contains(t, unary.Response.GetError().Message, "already known")
}

func TestFanoutRequestProcessorMaximumValueChoosesMax(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionCount"}, false, "eth")

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("up2", nil).Once()
	strategy.On("SelectUpstream", request).Return("up3", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector1 := mocks.NewConnectorMock()
	connector2 := mocks.NewConnectorMock()
	connector3 := mocks.NewConnectorMock()
	up1 := test_utils.TestEvmUpstream(connector1, upConfig(), mocks.NewMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(connector2, upConfig(), mocks.NewMethodsMock(), nil)
	up3 := test_utils.TestEvmUpstream(connector3, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up1).Once()
	upSupervisor.On("GetUpstream", "up2").Return(up2).Once()
	upSupervisor.On("GetUpstream", "up3").Return(up3).Once()

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x137"`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x139"`), protocol.JsonRpc)).Once()
	connector3.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x138"`), protocol.JsonRpc)).Once()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchMaximumValue)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, []byte(`"0x139"`), unary.Response.ResponseResult())
}

func TestFanoutRequestProcessorMaximumValueIgnoresInvalidWhenValidExists(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionCount"}, false, "eth")

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

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"10"`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xf"`), protocol.JsonRpc)).Once()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchMaximumValue)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.False(t, unary.Response.HasError())
	assert.Equal(t, []byte(`"0xf"`), unary.Response.ResponseResult())
}

func TestFanoutRequestProcessorMaximumValueAllInvalidReturnsError(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionCount"}, false, "eth")

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

	connector1.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"10"`), protocol.JsonRpc)).Once()
	connector2.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"latest"`), protocol.JsonRpc)).Once()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchMaximumValue)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.True(t, unary.Response.HasError())
	assert.Contains(t, unary.Response.GetError().Message, "invalid hex quantity")
}

func TestFanoutRequestProcessorNoSelectionReturnsSelectionError(t *testing.T) {
	ctx := context.Background()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "")
	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("", errors.New("selection failed")).Once()
	upSupervisor := mocks.NewUpstreamSupervisorMock()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchBroadcast)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.Equal(t, flow.NoUpstream, unary.UpstreamId)
	assert.True(t, unary.Response.HasError())
	assert.Contains(t, unary.Response.GetError().Message, "selection failed")
	upSupervisor.AssertNotCalled(t, "GetUpstream")
}

func TestFanoutRequestProcessorRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := protocol.NewUpstreamJsonRpcRequest("223", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "")
	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("up1", nil).Once()
	strategy.On("SelectUpstream", request).Return("", errors.New("done")).Once()

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	connector := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(connector, upConfig(), mocks.NewMethodsMock(), nil)
	upSupervisor.On("GetUpstream", "up1").Return(up).Maybe()
	connector.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xtx"`), protocol.JsonRpc)).Maybe()

	processor := flow.NewFanoutRequestProcessor(upSupervisor, specs.DispatchBroadcast)
	response := processor.ProcessRequest(ctx, strategy, request)
	unary := response.(*flow.UnaryResponse).ResponseWrapper

	assert.True(t, unary.Response.HasError())
}
