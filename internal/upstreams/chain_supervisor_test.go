package upstreams_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	upmethods "github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	eventuallyWait = time.Second
	eventuallyTick = 10 * time.Millisecond
)

var loadChainSupervisorMethodSpecsOnce sync.Once

func assertEventuallyEqual(t *testing.T, expected any, actual func() any) {
	t.Helper()

	assert.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(expected, actual())
	}, eventuallyWait, eventuallyTick)
}

func assertEventuallyElementsMatch(t *testing.T, expected []upstreams.AggregatedLabels, actual func() []upstreams.AggregatedLabels) {
	t.Helper()

	assert.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(canonicalAggregatedLabels(expected), canonicalAggregatedLabels(actual()))
	}, eventuallyWait, eventuallyTick)
}

func canonicalAggregatedLabels(labels []upstreams.AggregatedLabels) []string {
	canonical := make([]string, 0, len(labels))
	for _, item := range labels {
		keys := make([]string, 0, len(item.Labels))
		for key := range item.Labels {
			keys = append(keys, key)
		}
		slices.Sort(keys)

		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", key, item.Labels[key]))
		}

		canonical = append(canonical, fmt.Sprintf("%d|%v", item.Amount, parts))
	}

	slices.Sort(canonical)
	return canonical
}

func createEventWithLowerBounds(
	id string,
	status protocol.AvailabilityStatus,
	height uint64,
	methods upmethods.Methods,
	lowerBounds ...protocol.LowerBoundData,
) protocol.UpstreamEvent {
	lowerBoundsInfo := protocol.NewLowerBoundInfo()
	for _, bound := range lowerBounds {
		lowerBoundsInfo.AddLowerBound(bound)
	}

	state := protocol.DefaultUpstreamState(
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"",
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = protocol.Block{Height: height}
	state.LowerBoundsInfo = lowerBoundsInfo

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}
}

func createEventWithLabels(
	id string,
	status protocol.AvailabilityStatus,
	height uint64,
	methods upmethods.Methods,
	labels map[string]string,
) protocol.UpstreamEvent {
	labelsInfo := protocol.NewLabels()
	for key, value := range labels {
		labelsInfo.AddLabel(key, value)
	}

	state := protocol.DefaultUpstreamState(
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"",
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = protocol.Block{Height: height}
	state.Labels = labelsInfo

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}
}

func publishHeadEvent(
	chainSupervisor *upstreams.BaseChainSupervisor,
	id string,
	status protocol.AvailabilityStatus,
	head protocol.Block,
) {
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.HeadUpstreamEvent{
			Status: status,
			Head:   head,
		},
	})
}

func loadChainSupervisorMethodSpecs(t *testing.T) {
	t.Helper()

	loadChainSupervisorMethodSpecsOnce.Do(func() {
		err := specs.NewMethodSpecLoader().Load()
		require.NoError(t, err)
	})
}

func createEventWithCaps(
	id string,
	status protocol.AvailabilityStatus,
	height uint64,
	methods upmethods.Methods,
	caps mapset.Set[protocol.Cap],
) protocol.UpstreamEvent {
	state := protocol.DefaultUpstreamState(
		methods,
		caps,
		"",
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = protocol.Block{Height: height}

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}
}

func TestChainSupervisorUpdateHeadWithHeightFc(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	head := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("123"), blockchain.NewHashIdFromString("125"))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, head, methodsMock))
	publishHeadEvent(chainSupervisor, "id", protocol.Available, head)
	assertEventuallyEqual(t, head, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	head1 := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("123"), blockchain.NewHashIdFromString("125"))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id1", protocol.Available, head1, methodsMock))
	publishHeadEvent(chainSupervisor, "id1", protocol.Available, head1)
	assertEventuallyEqual(t, head, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	head2 := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("127"), blockchain.NewHashIdFromString("129"))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id3", protocol.Unavailable, head2, methodsMock))
	publishHeadEvent(chainSupervisor, "id3", protocol.Unavailable, head2)
	assertEventuallyEqual(t, head, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	head3 := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("1271"), blockchain.NewHashIdFromString("1291"))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, head3, methodsMock))
	publishHeadEvent(chainSupervisor, "id", protocol.Available, head3)
	assertEventuallyEqual(t, head3, func() any { return chainSupervisor.GetChainState().HeadData.Head })
}

func TestChainSupervisorUpdateHeadDoesNotPublishWrapperForEmptyChosenHead(t *testing.T) {
	fcMock := mocks.NewMockForkChoice()
	fcMock.On("Choose", "id", &protocol.HeadUpstreamEvent{
		Status: protocol.Available,
		Head:   protocol.NewBlockWithHeight(100),
	}).Return(true, protocol.ZeroBlock{})

	chainSupervisor := upstreams.NewBaseChainSupervisor(
		context.Background(),
		chains.ARBITRUM,
		fcMock,
		nil,
	)
	go chainSupervisor.Start()

	sub := chainSupervisor.SubscribeState(t.Name())
	publishHeadEvent(chainSupervisor, "id", protocol.Available, protocol.NewBlockWithHeight(100))

	assert.Eventually(t, func() bool {
		return chainSupervisor.GetChainState().HeadData.IsEmpty()
	}, eventuallyWait, eventuallyTick)

	assert.Never(t, func() bool {
		select {
		case wrapperEvent := <-sub.Events:
			return len(wrapperEvent.Wrappers) > 0
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond)
	fcMock.AssertExpectations(t)
}

func TestChainSupervisorUpdateHeadPublishesWrapperForNonEmptyChosenHead(t *testing.T) {
	head := protocol.NewBlockWithHeight(777)
	fcMock := mocks.NewMockForkChoice()
	fcMock.On("Choose", "id", &protocol.HeadUpstreamEvent{
		Status: protocol.Available,
		Head:   protocol.NewBlockWithHeight(100),
	}).Return(true, head)

	chainSupervisor := upstreams.NewBaseChainSupervisor(
		context.Background(),
		chains.ARBITRUM,
		fcMock,
		nil,
	)
	go chainSupervisor.Start()

	sub := chainSupervisor.SubscribeState(t.Name())
	publishHeadEvent(chainSupervisor, "id", protocol.Available, protocol.NewBlockWithHeight(100))

	assert.Eventually(t, func() bool {
		select {
		case wrapperEvent := <-sub.Events:
			require.Len(t, wrapperEvent.Wrappers, 1)
			headWrapper, ok := wrapperEvent.Wrappers[0].(*upstreams.HeadWrapper)
			return ok && headWrapper.Head.Equals(head)
		default:
			return false
		}
	}, eventuallyWait, eventuallyTick)

	assertEventuallyEqual(t, head, func() any { return chainSupervisor.GetChainState().HeadData.Head })
	fcMock.AssertExpectations(t)
}

func TestChainSupervisorTrackLags(t *testing.T) {
	tracker := dimensions.NewBaseDimensionTracker()
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), tracker)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockWithHeight(600), protocol.FinalizedBlock)
	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockWithHeight(700), protocol.FinalizedBlock)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id1", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock, blockInfo1))
	publishHeadEvent(chainSupervisor, "id1", protocol.Available, protocol.NewBlockWithHeight(100))
	assert.Eventually(t, func() bool {
		chainDims1 := tracker.GetChainDimensions(chains.ARBITRUM, "id1")
		return chainDims1.GetHeadLag() == 0 && chainDims1.GetFinalizationLag() == 0
	}, eventuallyWait, eventuallyTick)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id2", protocol.Available, protocol.NewBlockWithHeight(300), methodsMock, blockInfo2))
	publishHeadEvent(chainSupervisor, "id2", protocol.Available, protocol.NewBlockWithHeight(300))
	assert.Eventually(t, func() bool {
		chainDims1 := tracker.GetChainDimensions(chains.ARBITRUM, "id1")
		chainDims2 := tracker.GetChainDimensions(chains.ARBITRUM, "id2")

		return chainDims2.GetHeadLag() == 0 &&
			chainDims2.GetFinalizationLag() == 0 &&
			chainDims1.GetHeadLag() == 200 &&
			chainDims1.GetFinalizationLag() == 100
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorUpdateStatus(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock))
	assertEventuallyEqual(t, protocol.Available, func() any { return chainSupervisor.GetChainState().Status })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id1", protocol.Unavailable, protocol.NewBlockWithHeight(95), methodsMock))
	assertEventuallyEqual(t, protocol.Available, func() any { return chainSupervisor.GetChainState().Status })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Unavailable, protocol.NewBlockWithHeight(500), methodsMock))
	assertEventuallyEqual(t, protocol.Unavailable, func() any { return chainSupervisor.GetChainState().Status })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id12", protocol.Available, protocol.NewBlockWithHeight(95), methodsMock))
	assertEventuallyEqual(t, protocol.Available, func() any { return chainSupervisor.GetChainState().Status })
}

func TestChainSupervisorUnionUpstreamMethods(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods1 := mocks.NewMethodsMock()
	methods1.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))
	methods2 := mocks.NewMethodsMock()
	methods2.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test2"))
	methods3 := mocks.NewMethodsMock()
	methods3.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test2", "test5"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methods1))
	assertEventuallyEqual(t, mapset.NewThreadUnsafeSet[string]("test1"), func() any { return chainSupervisor.GetChainState().Methods.GetSupportedMethods() })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id2", protocol.Available, protocol.NewBlockWithHeight(100), methods2))
	assertEventuallyEqual(t, mapset.NewThreadUnsafeSet[string]("test1", "test2"), func() any { return chainSupervisor.GetChainState().Methods.GetSupportedMethods() })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id1", protocol.Available, protocol.NewBlockWithHeight(100), methods3))
	assertEventuallyEqual(t, mapset.NewThreadUnsafeSet[string]("test1", "test2", "test5"), func() any { return chainSupervisor.GetChainState().Methods.GetSupportedMethods() })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods1))
	assertEventuallyEqual(t, mapset.NewThreadUnsafeSet[string]("test2", "test5"), func() any { return chainSupervisor.GetChainState().Methods.GetSupportedMethods() })
}

func TestChainSupervisorUnionUpstreamBlockInfo(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockWithHeight(1000), protocol.FinalizedBlock)

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo1))
	assertEventuallyEqual(t, uint64(1000), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })

	blockInfo1.AddBlock(protocol.NewBlockWithHeight(2000), protocol.FinalizedBlock)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo1))
	assertEventuallyEqual(t, uint64(2000), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })

	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockWithHeight(500), protocol.FinalizedBlock)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id1", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo2))
	assertEventuallyEqual(t, uint64(2000), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })

	blockInfo3 := protocol.NewBlockInfo()
	blockInfo3.AddBlock(protocol.NewBlockWithHeight(50000), protocol.FinalizedBlock)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id5", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	assertEventuallyEqual(t, uint64(50000), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id5", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	assertEventuallyEqual(t, uint64(2000), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEventWithBlockData("id", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	assertEventuallyEqual(t, uint64(500), func() any { return chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height })
}

func TestChainSupervisorRemoveUpstreamState(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	assert.Eventually(t, func() bool {
		return chainSupervisor.GetUpstreamState("id") != nil
	}, eventuallyWait, eventuallyTick)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id"))
	assert.Eventually(t, func() bool {
		return chainSupervisor.GetUpstreamState("id") == nil
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorRemoveUpstreamRecomputesHead(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	go chainSupervisor.Start()

	firstHead := protocol.NewBlockWithHeight(100)
	secondHead := protocol.NewBlockWithHeight(200)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id1", protocol.Available, firstHead, methods))
	publishHeadEvent(chainSupervisor, "id1", protocol.Available, firstHead)
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id2", protocol.Available, secondHead, methods))
	publishHeadEvent(chainSupervisor, "id2", protocol.Available, secondHead)
	assertEventuallyEqual(t, secondHead, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id2"))
	assertEventuallyEqual(t, firstHead, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id1"))
	assert.Eventually(t, func() bool {
		return chainSupervisor.GetChainState().HeadData.IsEmpty()
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorRemoveUpstreamWithoutTrackedHeadDoesNotResetChosenHead(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	go chainSupervisor.Start()

	chosenHead := protocol.NewBlockWithHeight(100)

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("tracked", protocol.Available, chosenHead, methods))
	publishHeadEvent(chainSupervisor, "tracked", protocol.Available, chosenHead)
	assertEventuallyEqual(t, chosenHead, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	// This upstream is present in state, but fork choice never saw a head event for it.
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("state-only", protocol.Available, protocol.NewBlockWithHeight(200), methods))
	assertEventuallyEqual(t, chosenHead, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("state-only"))
	assertEventuallyEqual(t, chosenHead, func() any { return chainSupervisor.GetChainState().HeadData.Head })
}

func TestChainSupervisorGetChainAndUpstreamIds(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id2", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id1", protocol.Available, protocol.NewBlockWithHeight(101), methods))

	assert.Equal(t, chains.ARBITRUM, chainSupervisor.GetChain())
	assert.Eventually(t, func() bool {
		return assert.ObjectsAreEqual([]string{"id1", "id2"}, chainSupervisor.GetUpstreamIds())
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorGetSortedUpstreamIds(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("up-1", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("up-2", protocol.Unavailable, protocol.NewBlockWithHeight(300), methods))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("up-3", protocol.Available, protocol.NewBlockWithHeight(200), methods))

	assert.Eventually(t, func() bool {
		ids := chainSupervisor.GetSortedUpstreamIds(
			func(_ string, state *protocol.UpstreamState) bool {
				return state.Status == protocol.Available
			},
			func(a, b lo.Tuple2[string, *protocol.UpstreamState]) int {
				if a.B.HeadData.Height < b.B.HeadData.Height {
					return -1
				}
				if a.B.HeadData.Height > b.B.HeadData.Height {
					return 1
				}
				return 0
			},
		)

		return assert.ObjectsAreEqual([]string{"up-1", "up-3"}, ids)
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorProcessSubMethods(t *testing.T) {
	loadChainSupervisorMethodSpecs(t)

	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ETHEREUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	sub := chainSupervisor.SubscribeState(t.Name())
	expectedSubMethods := specs.GetSubMethods(chains.GetMethodSpecNameByChain(chains.ETHEREUM))

	chainSupervisor.PublishUpstreamEvent(createEventWithCaps(
		"id1",
		protocol.Available,
		100,
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap),
	))

	assert.Eventually(t, func() bool {
		return chainSupervisor.GetChainState().SubMethods.Equal(expectedSubMethods)
	}, eventuallyWait, eventuallyTick)

	var wrapperEvent *upstreams.ChainSupervisorStateWrapperEvent
	assert.Eventually(t, func() bool {
		select {
		case wrapperEvent = <-sub.Events:
			for _, wrapper := range wrapperEvent.Wrappers {
				subMethodsWrapper, ok := wrapper.(*upstreams.SubMethodsWrapper)
				if ok {
					return assert.ElementsMatch(t, expectedSubMethods.ToSlice(), subMethodsWrapper.SubMethods)
				}
			}
			return false
		default:
			return false
		}
	}, eventuallyWait, eventuallyTick)

	chainSupervisor.PublishUpstreamEvent(createEventWithCaps(
		"id1",
		protocol.Unavailable,
		100,
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap),
	))

	assert.Eventually(t, func() bool {
		return chainSupervisor.GetChainState().SubMethods.Cardinality() == 0
	}, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorLowerBoundsInitialStateIsEmpty(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)

	assert.Empty(t, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsSingleAvailableUpstream(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	slotBound := protocol.NewLowerBoundData(120, 1000, protocol.SlotBound)
	stateBound := protocol.NewLowerBoundData(450, 1000, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id", protocol.Available, 100, methods, slotBound, stateBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.SlotBound:  slotBound,
		protocol.StateBound: stateBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsUseMinimumBoundPerTypeAcrossAvailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	slotBound1 := protocol.NewLowerBoundData(200, 1000, protocol.SlotBound)
	stateBound1 := protocol.NewLowerBoundData(500, 1000, protocol.StateBound)
	slotBound2 := protocol.NewLowerBoundData(150, 1010, protocol.SlotBound)
	stateBound2 := protocol.NewLowerBoundData(700, 1010, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id1", protocol.Available, 100, methods, slotBound1, stateBound1))
	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id2", protocol.Available, 110, methods, slotBound2, stateBound2))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.SlotBound:  slotBound2,
		protocol.StateBound: stateBound1,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsIgnoreUnavailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	availableBound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	unavailableBetterBound := protocol.NewLowerBoundData(100, 1010, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("available", protocol.Available, 100, methods, availableBound))
	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("unavailable", protocol.Unavailable, 100, methods, unavailableBetterBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: availableBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsIgnoreUpstreamsWithoutLowerBoundsInfo(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	bound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("with-bounds", protocol.Available, 100, methods, bound))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("without-bounds", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: bound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsUpdateExistingUpstreamState(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	initialBound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	updatedBound := protocol.NewLowerBoundData(200, 1010, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id", protocol.Available, 100, methods, initialBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: initialBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id", protocol.Available, 120, methods, updatedBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: updatedBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsRecomputeWhenUpstreamBecomesUnavailable(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	lowerBound := protocol.NewLowerBoundData(200, 1000, protocol.StateBound)
	higherBound := protocol.NewLowerBoundData(500, 1010, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id1", protocol.Available, 100, methods, lowerBound))
	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id2", protocol.Available, 100, methods, higherBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: lowerBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id1", protocol.Unavailable, 100, methods, lowerBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: higherBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })
}

func TestChainSupervisorLowerBoundsRecomputeWhenUpstreamRemoved(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	lowerBound := protocol.NewLowerBoundData(200, 1000, protocol.StateBound)
	higherBound := protocol.NewLowerBoundData(500, 1010, protocol.StateBound)

	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id1", protocol.Available, 100, methods, lowerBound))
	chainSupervisor.PublishUpstreamEvent(createEventWithLowerBounds("id2", protocol.Available, 100, methods, higherBound))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: lowerBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id1"))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: higherBound,
	}, func() any { return chainSupervisor.GetChainState().LowerBounds })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id2"))
	assertEventuallyEqual(t, map[protocol.LowerBoundType]protocol.LowerBoundData{}, func() any {
		return chainSupervisor.GetChainState().LowerBounds
	})
}

func TestChainSupervisorLabelsInitialStateIsEmpty(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)

	assert.Empty(t, chainSupervisor.GetChainState().ChainLabels)
}

func TestChainSupervisorLabelsSingleAvailableUpstream(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id", protocol.Available, 100, methods, map[string]string{
		"client_type":    "solana",
		"client_version": "1.18.23",
	}))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, map[string]string{
			"client_type":    "solana",
			"client_version": "1.18.23",
		}),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })
}

func TestChainSupervisorLabelsAggregateIdenticalLabelsAcrossAvailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	commonLabels := map[string]string{
		"client_type":    "solana",
		"client_version": "1.18.23",
	}

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id1", protocol.Available, 100, methods, commonLabels))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id2", protocol.Available, 120, methods, commonLabels))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, commonLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })
}

func TestChainSupervisorLabelsIgnoreUnavailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	availableLabels := map[string]string{
		"client_type": "solana",
	}
	unavailableLabels := map[string]string{
		"client_type": "agave",
	}

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("available", protocol.Available, 100, methods, availableLabels))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("unavailable", protocol.Unavailable, 100, methods, unavailableLabels))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, availableLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })
}

func TestChainSupervisorLabelsIgnoreUpstreamsWithoutLabels(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	labels := map[string]string{
		"client_type": "solana",
	}

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("with-labels", protocol.Available, 100, methods, labels))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("without-labels", protocol.Available, protocol.NewBlockWithHeight(100), methods))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, labels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })
}

func TestChainSupervisorLabelsRecomputeWhenUpstreamBecomesUnavailable(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	firstLabels := map[string]string{
		"client_type": "solana",
	}
	secondLabels := map[string]string{
		"client_type": "agave",
	}

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id1", protocol.Available, 100, methods, firstLabels))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id2", protocol.Available, 100, methods, secondLabels))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, firstLabels),
		upstreams.NewAggregatedLabels(1, secondLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id1", protocol.Unavailable, 100, methods, firstLabels))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, secondLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })
}

func TestChainSupervisorLabelsRecomputeWhenUpstreamRemoved(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	firstLabels := map[string]string{
		"client_type": "solana",
	}
	secondLabels := map[string]string{
		"client_type": "agave",
	}

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id1", protocol.Available, 100, methods, firstLabels))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("id2", protocol.Available, 100, methods, secondLabels))

	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, firstLabels),
		upstreams.NewAggregatedLabels(1, secondLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id1"))
	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, secondLabels),
	}, func() []upstreams.AggregatedLabels { return chainSupervisor.GetChainState().ChainLabels })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id2"))
	assertEventuallyElementsMatch(t, []upstreams.AggregatedLabels{}, func() []upstreams.AggregatedLabels {
		return chainSupervisor.GetChainState().ChainLabels
	})
}
