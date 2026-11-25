package emerald_test

import (
	"context"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func describeSupervisor(chainSupervisors ...upstreams.ChainSupervisor) *mocks.UpstreamSupervisorMock {
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("GetChainSupervisors").Return(chainSupervisors)
	return upstreamSupervisor
}

func grpcId(chain chains.Chain) dshackle.ChainRef {
	return dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId)
}

func describeSingleChain(t *testing.T, state upstreams.ChainSupervisorState) *dshackle.DescribeChain {
	t.Helper()

	response := emerald.DescribeChains(describeSupervisor(newFakeChainSupervisor(chains.ARBITRUM, state)))
	require.Len(t, response.GetChains(), 1)

	return response.GetChains()[0]
}

func TestDescribeChains_MapsChainState(t *testing.T) {
	state := newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(123), []string{"eth_call"})
	state.SubMethods = mapset.NewThreadUnsafeSet("newHeads")
	state.ChainLabels = []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, map[string]string{"region": "us-east-1"}),
	}

	response := emerald.DescribeChains(describeSupervisor(newFakeChainSupervisor(chains.ARBITRUM, state)))

	assert.True(t, strings.HasPrefix(response.GetBuildInfo().GetVersion(), "nodecore/"))
	require.Len(t, response.GetChains(), 1)
	described := response.GetChains()[0]
	assert.Equal(t, grpcId(chains.ARBITRUM), described.GetChain())
	assert.Equal(t, grpcId(chains.ARBITRUM), described.GetStatus().GetChain())
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_OK, described.GetStatus().GetAvailability())
	assert.Equal(t, int64(123), described.GetCurrentHeight())
	assert.Equal(t, []string{"eth_call"}, described.GetSupportedMethods())
	assert.Equal(t, []string{"newHeads"}, described.GetSupportedSubscriptions())
	require.Len(t, described.GetNodes(), 1)
	assert.Equal(t, uint32(2), described.GetNodes()[0].GetQuorum())
	require.Len(t, described.GetNodes()[0].GetLabels(), 1)
	assert.Equal(t, "region", described.GetNodes()[0].GetLabels()[0].GetName())
	assert.Equal(t, "us-east-1", described.GetNodes()[0].GetLabels()[0].GetValue())
}

func TestDescribeChains_LeavesUnmappedFieldsEmpty(t *testing.T) {
	// every source an unmapped field could be filled from is populated
	state := newChainState(chains.ARBITRUM, protocol.NewBlockWithHeights(123, 456), []string{"eth_call"})
	state.ChainLabels = []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, map[string]string{"region": "us-east-1"}),
	}
	state.LowerBounds = map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: protocol.NewLowerBoundData(100, 1700000000, protocol.StateBound),
		protocol.SlotBound:  protocol.NewLowerBoundData(200, 1700000000, protocol.SlotBound),
	}
	state.Caps = mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap)

	described := describeSingleChain(t, state)

	assert.Zero(t, described.GetStatus().GetQuorum())
	assert.Empty(t, described.GetCapabilities())
	assert.Empty(t, described.GetExcludedMethods())
	assert.Zero(t, described.GetCurrentLowerBlock())
	assert.Zero(t, described.GetCurrentLowerSlot())
}

func TestDescribeChains_ListsChainWithoutHead(t *testing.T) {
	state := newChainState(chains.ARBITRUM, protocol.ZeroBlock{}, nil)

	described := describeSingleChain(t, state)

	assert.Equal(t, grpcId(chains.ARBITRUM), described.GetChain())
	assert.Zero(t, described.GetCurrentHeight())
	assert.Empty(t, described.GetSupportedMethods())
	assert.Empty(t, described.GetSupportedSubscriptions())
	assert.Empty(t, described.GetNodes())
}

func TestDescribeChains_NoChains(t *testing.T) {
	response := emerald.DescribeChains(describeSupervisor())

	assert.Empty(t, response.GetChains())
	assert.NotEmpty(t, response.GetBuildInfo().GetVersion())
}

func TestDescribeChains_MapsUnavailableStatus(t *testing.T) {
	state := newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(123), []string{"eth_call"})
	state.Status = protocol.Unavailable

	described := describeSingleChain(t, state)

	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, described.GetStatus().GetAvailability())
}

func TestDescribeChains_SkipsChainWithoutGrpcId(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	require.Zero(t, grpcId(chains.Unknown))

	response := emerald.DescribeChains(describeSupervisor(
		newFakeChainSupervisor(chains.Unknown, newChainState(chains.Unknown, head, []string{"eth_call"})),
		newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"})),
	))

	require.Len(t, response.GetChains(), 1)
	assert.Equal(t, grpcId(chains.ARBITRUM), response.GetChains()[0].GetChain())
}

func TestDescribeChains_SortsChainsMethodsAndSubscriptions(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	lower, higher := chains.ETHEREUM, chains.ARBITRUM
	if grpcId(lower) > grpcId(higher) {
		lower, higher = higher, lower
	}
	require.NotEqual(t, grpcId(lower), grpcId(higher))

	// enough elements that a random iteration order is practically never sorted
	higherState := newChainState(higher, head, []string{"eth_getLogs", "eth_call", "eth_blockNumber", "eth_chainId", "eth_getBalance", "eth_gasPrice"})
	higherState.SubMethods = mapset.NewThreadUnsafeSet("newHeads", "logs", "syncing", "newPendingTransactions", "drpc_pendingTransactions", "alchemy_minedTransactions")

	response := emerald.DescribeChains(describeSupervisor(
		newFakeChainSupervisor(higher, higherState),
		newFakeChainSupervisor(lower, newChainState(lower, head, []string{"eth_call"})),
	))

	require.Len(t, response.GetChains(), 2)
	assert.Equal(t, grpcId(lower), response.GetChains()[0].GetChain())
	assert.Equal(t, grpcId(higher), response.GetChains()[1].GetChain())
	assert.Equal(t,
		[]string{"eth_blockNumber", "eth_call", "eth_chainId", "eth_gasPrice", "eth_getBalance", "eth_getLogs"},
		response.GetChains()[1].GetSupportedMethods(),
	)
	assert.Equal(t,
		[]string{"alchemy_minedTransactions", "drpc_pendingTransactions", "logs", "newHeads", "newPendingTransactions", "syncing"},
		response.GetChains()[1].GetSupportedSubscriptions(),
	)
}

func TestDescribe_ReturnsChainsFromSupervisor(t *testing.T) {
	state := newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(123), []string{"eth_call"})
	appCtx := &server_ctx.ApplicationServerContext{
		UpstreamSupervisor: describeSupervisor(newFakeChainSupervisor(chains.ARBITRUM, state)),
	}
	service := emerald.NewGrpcBlockchainService(appCtx, nil, signature.NewDisabledSigner())

	response, err := service.Describe(context.Background(), &dshackle.DescribeRequest{})

	require.NoError(t, err)
	require.Len(t, response.GetChains(), 1)
	assert.Equal(t, int64(123), response.GetChains()[0].GetCurrentHeight())
}
