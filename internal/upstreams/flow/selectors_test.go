package flow

import (
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectorAndPropagatesHeightLatestSortHint(t *testing.T) {
	matchers, order := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestAndSelector{
		Children: []protocol.RequestSelector{
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
			protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest},
		},
	}}, nil, nil)

	require.Len(t, matchers, 1)
	assert.NotNil(t, order)
}

func TestSelectorAndPropagatesLowerHeightSortHint(t *testing.T) {
	matchers, order := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestAndSelector{
		Children: []protocol.RequestSelector{
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
			protocol.RequestLowerHeightSelector{Height: 0, LowerBoundType: protocol.StateBound, TimeOffset: 9},
		},
	}}, nil, nil)

	require.Len(t, matchers, 1)
	assert.NotNil(t, order)

}

func TestSelectorOrAndNotRejectSortBearingChildren(t *testing.T) {
	orMatchers, orOrder := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestOrSelector{
		Children: []protocol.RequestSelector{
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
			protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest},
		},
	}}, nil, nil)
	require.Len(t, orMatchers, 1)
	assert.Equal(t, SelectorType, orMatchers[0].Match("up", &protocol.UpstreamState{}).Type())
	assert.Nil(t, orOrder)

	notMatchers, notOrder := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestNotSelector{Child: protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}}}, nil, nil)
	require.Len(t, notMatchers, 1)
	assert.Equal(t, SelectorType, notMatchers[0].Match("up", &protocol.UpstreamState{}).Type())
	assert.Nil(t, notOrder)
}

func TestSelectorOrderUsesDistinctSafeAndFinalizedBlocks(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	publishStateWithBlocks(chSup, "up-safe", 100, 90, 50)
	publishStateWithBlocks(chSup, "up-finalized", 100, 40, 80)

	_, safeOrder := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestBlockTagSelector{Tag: protocol.BlockTagSafe}}, nil, chSup)
	_, finalizedOrder := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestBlockTagSelector{Tag: protocol.BlockTagFinalized}}, nil, chSup)

	assert.Equal(t, []string{"up-safe", "up-finalized"}, safeOrder([]string{"up-finalized", "up-safe"}))
	assert.Equal(t, []string{"up-finalized", "up-safe"}, finalizedOrder([]string{"up-finalized", "up-safe"}))
}

func TestSelectorOrderPrecomputesHeadKeysOnce(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	publishStateWithBlocks(chSup, "up-high", 200, 0, 0)
	publishStateWithBlocks(chSup, "up-low", 100, 0, 0)

	_, order := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}}, nil, chSup)

	assert.Equal(t, []string{"up-high", "up-low"}, order([]string{"up-low", "up-high"}))
}

func publishStateWithBlocks(chainSupervisor upstreams.ChainSupervisor, upId string, head, safe, finalized uint64) {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getBalance"))
	methodsMock.On("HasMethod", "eth_getBalance").Return(true)
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "index", nil, nil)
	state.Status = protocol.Available
	state.HeadData = protocol.Block{Height: head}
	if safe > 0 {
		state.BlockInfo.AddBlock(protocol.NewBlockWithHeight(safe), protocol.SafeBlock)
	}
	if finalized > 0 {
		state.BlockInfo.AddBlock(protocol.NewBlockWithHeight(finalized), protocol.FinalizedBlock)
	}
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: upId, EventType: &protocol.StateUpstreamEvent{State: &state}})
	time.Sleep(10 * time.Millisecond)
}
