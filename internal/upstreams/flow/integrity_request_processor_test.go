package flow_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestIntegrityRequestProcessor_QuorumRequested_Bypasses(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	processor := NewRequestProcessorMock()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	// eth_blockNumber would normally trigger integrity re-check, but quorum
	// in the context must short-circuit that and delegate straight through.
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil, chains.ARBITRUM)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	integrityProcessor := flow.NewIntegrityRequestProcessor(chains.ARBITRUM, upSupervisor, processor)

	processor.On("ProcessRequest", ctx, strategy, request).Return(&flow.UnaryResponse{})

	resp := integrityProcessor.ProcessRequest(ctx, strategy, request)

	processor.AssertExpectations(t)
	upSupervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
	upSupervisor.AssertNotCalled(t, "GetUpstream", mock.Anything)
	upSupervisor.AssertNotCalled(t, "GetExecutor")
	strategy.AssertNotCalled(t, "SelectUpstream", mock.Anything)

	assert.Equal(t, &flow.UnaryResponse{}, resp)
}

func TestIntegrityRequestProcessorAnyMethodNoProcessed(t *testing.T) {
	strategy := mocks.NewMockStrategy()
	processor := NewRequestProcessorMock()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("any", nil, chains.ARBITRUM)
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
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil, chains.ARBITRUM)
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
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil, chains.ARBITRUM)
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

// The integrity retry walks upstreams by height, but must still honor the
// request's selectors: an API key restricted to archive nodes never gets its
// stale answer re-fetched from a higher full node.
func TestIntegrityRequestProcessorRetryHonorsSelectors(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string](specs.EthBlockNumber))
	methodsMock.On("HasMethod", mock.Anything).Return(true)
	chainSupervisor := upstreams.NewGenericChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()
	publishEventWithHead(chainSupervisor, test_utils.CreateEvent("stale-archive", protocol.Available, protocol.NewBlockWithHeight(5), methodsMock))
	publishEventWithHead(chainSupervisor, test_utils.CreateEvent("high-archive", protocol.Available, protocol.NewBlockWithHeight(108), methodsMock))
	publishEventWithHead(chainSupervisor, test_utils.CreateEvent("high-full", protocol.Available, protocol.NewBlockWithHeight(150), methodsMock))
	time.Sleep(10 * time.Millisecond)

	request, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil, chains.ARBITRUM)
	request.AppendSelectors(protocol.RequestGroupLabelSelector{Labels: []string{"archive"}})

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	upSupervisor.On("GetChainSupervisor", chains.ARBITRUM).Return(chainSupervisor)
	for _, up := range []struct{ id, groupLabel, answer string }{
		{"stale-archive", "archive", `"0x5"`},
		{"high-archive", "archive", `"0x6c"`},
		{"high-full", "full", `"0x96"`},
	} {
		connector := mocks.NewConnectorMock()
		connector.On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(up.answer), protocol.JsonRpc)).Maybe()
		upConf := upConfig()
		upConf.GroupLabels = []string{up.groupLabel}
		upSupervisor.On("GetUpstream", up.id).Return(test_utils.TestEvmUpstream(connector, upConf, methodsMock, nil))
	}
	// TestEvmUpstream stamps every response with the upstream id "id".
	upSupervisor.On("GetUpstream", "id").Return(test_utils.TestEvmUpstream(mocks.NewConnectorMock(), upConfig(), methodsMock, nil)).Maybe()

	strategy := mocks.NewMockStrategy()
	strategy.On("SelectUpstream", request).Return("stale-archive", nil).Once()

	integrityProcessor := flow.NewIntegrityRequestProcessor(chains.ARBITRUM, upSupervisor, NewRequestProcessorMock())
	resp := integrityProcessor.ProcessRequest(context.Background(), strategy, request).(*flow.UnaryResponse)

	assert.False(t, resp.ResponseWrapper.Response.HasError())
	assert.Equal(t, []byte(`"0x6c"`), resp.ResponseWrapper.Response.ResponseResult())
}
