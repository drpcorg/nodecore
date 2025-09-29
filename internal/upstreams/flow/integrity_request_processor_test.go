package flow_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestIntegrityRequestProcessorAnyMethodNoProcessed(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	processor := NewRequestProcessorMock()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("any", nil)
	ctx := context.Background()
	integrityProcessor := flow.NewIntegrityRequestProcessor(chains.ARBITRUM, upSupervisor, processor)

	processor.On("ProcessRequest", ctx, strategy, request).Return(&flow.UnaryResponse{})

	resp := integrityProcessor.ProcessRequest(ctx, strategy, request)

	processor.AssertExpectations(t)
	upSupervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
	upSupervisor.AssertNotCalled(t, "GetUpstream", mock.Anything)
	strategy.AssertNotCalled(t, "SelectUpstream", mock.Anything)

	assert.Equal(t, &flow.UnaryResponse{}, resp)
}

func TestIntegrityRequestProcessorNotHandledIfErr(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	processor := NewRequestProcessorMock()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	ctx := context.Background()
	integrityProcessor := flow.NewIntegrityRequestProcessor(chains.ARBITRUM, upSupervisor, processor)

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("", protocol.NoAvailableUpstreamsError())

	resp := integrityProcessor.ProcessRequest(ctx, strategy, request)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	processor.AssertNotCalled(t, "ProcessRequest", mock.Anything, mock.Anything, mock.Anything)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.NoAvailableUpstreamsError(), request.RequestType()),
	}

	assert.Equal(t, &flow.UnaryResponse{ResponseWrapper: expected}, resp)
}

func TestIntegrityRequestProcessorNotHandledIfRespWithErr(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewConnectorMock()
	ctx := context.Background()
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	responseHolder := protocol.NewTotalFailure(request, protocol.RequestTimeoutError())
	processor := NewRequestProcessorMock()
	integrityProcessor := flow.NewIntegrityRequestProcessor(chains.ARBITRUM, upSupervisor, processor)

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", ctx, request).Return(responseHolder)

	resp := integrityProcessor.ProcessRequest(ctx, strategy, request)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	apiConnector.AssertExpectations(t)
	processor.AssertNotCalled(t, "ProcessRequest", mock.Anything, mock.Anything, mock.Anything)
	upSupervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailure(request, protocol.RequestTimeoutError()),
	}

	assert.Equal(t, &flow.UnaryResponse{ResponseWrapper: expected}, resp)
}
