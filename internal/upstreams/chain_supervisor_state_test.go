package upstreams_test

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMethodsMock(supported ...string) *mocks.MethodsMock {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string](supported...))
	return methodsMock
}

func newChainSupervisorState() upstreams.ChainSupervisorState {
	return upstreams.ChainSupervisorState{
		Status:      protocol.Available,
		HeadData:    upstreams.NewChainHeadData(protocol.NewBlockWithHeight(10), "upstream-1"),
		Methods:     newMethodsMock("eth_call"),
		Blocks:      map[protocol.BlockType]protocol.Block{protocol.FinalizedBlock: protocol.NewBlockWithHeight(100)},
		LowerBounds: map[protocol.LowerBoundType]protocol.LowerBoundData{protocol.StateBound: protocol.NewLowerBoundData(50, 1000, protocol.StateBound)},
		ChainLabels: []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, map[string]string{"region": "us-east-1"})},
		SubMethods:  mapset.NewThreadUnsafeSet[string]("logs"),
	}
}

func TestNewChainHeadData(t *testing.T) {
	head := protocol.NewBlockWithHeights(10, 20)

	result := upstreams.NewChainHeadData(head, "upstream-1")

	assert.Equal(t, upstreams.ChainHeadData{
		Head:       head,
		UpstreamId: "upstream-1",
	}, result)
}

func TestChainHeadDataIsEmpty(t *testing.T) {
	assert.True(t, upstreams.NewChainHeadData(protocol.Block{}, "upstream-1").IsEmpty())
	assert.True(t, upstreams.NewChainHeadData(protocol.Block{Slot: 10}, "upstream-1").IsEmpty())
	assert.False(t, upstreams.NewChainHeadData(protocol.NewBlockWithHeight(10), "upstream-1").IsEmpty())
}

func TestNewAggregatedLabels(t *testing.T) {
	result := upstreams.NewAggregatedLabels(2, map[string]string{"region": "us-east-1"})

	assert.Equal(t, upstreams.AggregatedLabels{
		Amount: 2,
		Labels: map[string]string{"region": "us-east-1"},
	}, result)
}

func TestAggregatedLabelsEquals(t *testing.T) {
	base := upstreams.NewAggregatedLabels(2, map[string]string{
		"region": "us-east-1",
		"tier":   "archive",
	})

	assert.True(t, base.Equals(upstreams.NewAggregatedLabels(2, map[string]string{
		"tier":   "archive",
		"region": "us-east-1",
	})))
	assert.False(t, base.Equals(upstreams.NewAggregatedLabels(1, map[string]string{
		"region": "us-east-1",
		"tier":   "archive",
	})))
	assert.False(t, base.Equals(upstreams.NewAggregatedLabels(2, map[string]string{
		"region": "eu-west-1",
		"tier":   "archive",
	})))
}

func TestCompareAggregatedLabels(t *testing.T) {
	left := []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, map[string]string{"region": "us-east-1"}),
		upstreams.NewAggregatedLabels(2, map[string]string{"tier": "archive"}),
	}
	right := []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, map[string]string{"tier": "archive"}),
		upstreams.NewAggregatedLabels(1, map[string]string{"region": "us-east-1"}),
	}

	assert.True(t, upstreams.CompareAggregatedLabels(left, right))
	assert.False(t, upstreams.CompareAggregatedLabels(left, right[:1]))
	assert.False(t, upstreams.CompareAggregatedLabels(left, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, map[string]string{"region": "us-east-1"}),
		upstreams.NewAggregatedLabels(2, map[string]string{"tier": "full"}),
	}))
}

func TestChainSupervisorStateCompare_Status(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.Status = protocol.Unavailable

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	statusWrapper, ok := wrappers[0].(*upstreams.StatusWrapper)
	require.True(t, ok)
	assert.Equal(t, protocol.Unavailable, statusWrapper.Status)
}

func TestChainSupervisorStateCompare_Methods(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.Methods = newMethodsMock("eth_call", "eth_getBalance")

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	methodsWrapper, ok := wrappers[0].(*upstreams.MethodsWrapper)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"eth_call", "eth_getBalance"}, methodsWrapper.Methods)
}

func TestChainSupervisorStateCompare_Blocks(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.Blocks = map[protocol.BlockType]protocol.Block{
		protocol.FinalizedBlock: protocol.NewBlockWithHeight(200),
	}

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	blocksWrapper, ok := wrappers[0].(*upstreams.BlocksWrapper)
	require.True(t, ok)
	assert.Equal(t, newState.Blocks, blocksWrapper.Blocks)
}

func TestChainSupervisorStateCompare_LowerBounds(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.LowerBounds = map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: protocol.NewLowerBoundData(40, 1010, protocol.StateBound),
	}

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	lowerBoundsWrapper, ok := wrappers[0].(*upstreams.LowerBoundsWrapper)
	require.True(t, ok)
	assert.ElementsMatch(t, []protocol.LowerBoundData{
		protocol.NewLowerBoundData(40, 1010, protocol.StateBound),
	}, lowerBoundsWrapper.LowerBounds)
}

func TestChainSupervisorStateCompare_Labels(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.ChainLabels = []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, map[string]string{"region": "eu-west-1"}),
	}

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	labelsWrapper, ok := wrappers[0].(*upstreams.LabelsWrapper)
	require.True(t, ok)
	assert.True(t, upstreams.CompareAggregatedLabels(newState.ChainLabels, labelsWrapper.Labels))
}

func TestChainSupervisorStateCompare_SubMethods(t *testing.T) {
	oldState := newChainSupervisorState()
	newState := newChainSupervisorState()
	newState.SubMethods = mapset.NewThreadUnsafeSet[string]("logs", "blocks")

	wrappers := oldState.Compare(newState)

	require.Len(t, wrappers, 1)
	subMethodsWrapper, ok := wrappers[0].(*upstreams.SubMethodsWrapper)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"logs", "blocks"}, subMethodsWrapper.SubMethods)
}

func TestChainSupervisorStateCompare_DoesNotEmitHeadWrapperForEmptyNewHead(t *testing.T) {
	oldState := upstreams.ChainSupervisorState{
		HeadData:   upstreams.NewChainHeadData(protocol.NewBlockWithHeight(10), "upstream-1"),
		Methods:    newMethodsMock(),
		Blocks:     map[protocol.BlockType]protocol.Block{},
		SubMethods: mapset.NewThreadUnsafeSet[string](),
	}
	newState := upstreams.ChainSupervisorState{
		HeadData:   upstreams.NewChainHeadData(protocol.Block{}, "upstream-2"),
		Methods:    newMethodsMock(),
		Blocks:     map[protocol.BlockType]protocol.Block{},
		SubMethods: mapset.NewThreadUnsafeSet[string](),
	}

	wrappers := oldState.Compare(newState)

	assert.Empty(t, wrappers)
}
