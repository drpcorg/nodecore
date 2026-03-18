package upstreams_test

import (
	"context"
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
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

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

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &protocol.UpstreamState{
				Status: status,
				HeadData: protocol.Block{
					Height: height,
				},
				UpstreamMethods: methods,
				LowerBoundsInfo: lowerBoundsInfo,
			},
		},
	}
}

func TestChainSupervisorUpdateHeadWithHeightFc(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	head := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("123"), blockchain.NewHashIdFromString("125"))
	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, head, methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, head, chainSupervisor.GetChainState().HeadData.Head)

	head1 := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("123"), blockchain.NewHashIdFromString("125"))
	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Available, head1, methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, head, chainSupervisor.GetChainState().HeadData.Head)

	head2 := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("127"), blockchain.NewHashIdFromString("129"))
	chainSupervisor.Publish(test_utils.CreateEvent("id3", protocol.Unavailable, head2, methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, head, chainSupervisor.GetChainState().HeadData.Head)

	head3 := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("1271"), blockchain.NewHashIdFromString("1291"))
	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, head3, methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, head3, chainSupervisor.GetChainState().HeadData.Head)
}

func TestChainSupervisorTrackLags(t *testing.T) {
	tracker := dimensions.NewBaseDimensionTracker()
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), tracker)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockWithHeight(600), protocol.FinalizedBlock)
	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockWithHeight(700), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id1", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock, blockInfo1))
	time.Sleep(20 * time.Millisecond)

	chainDims1 := tracker.GetChainDimensions(chains.ARBITRUM, "id1")
	assert.Equal(t, uint64(0), chainDims1.GetHeadLag())
	assert.Equal(t, uint64(0), chainDims1.GetFinalizationLag())

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id2", protocol.Available, protocol.NewBlockWithHeight(300), methodsMock, blockInfo2))
	time.Sleep(20 * time.Millisecond)

	chainDims1 = tracker.GetChainDimensions(chains.ARBITRUM, "id1")
	chainDims2 := tracker.GetChainDimensions(chains.ARBITRUM, "id2")

	assert.Equal(t, uint64(0), chainDims2.GetHeadLag())
	assert.Equal(t, uint64(0), chainDims2.GetFinalizationLag())
	assert.Equal(t, uint64(200), chainDims1.GetHeadLag())
	assert.Equal(t, uint64(100), chainDims1.GetFinalizationLag())
}

func TestChainSupervisorUpdateStatus(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Unavailable, protocol.NewBlockWithHeight(95), methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Unavailable, protocol.NewBlockWithHeight(500), methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, protocol.Unavailable, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id12", protocol.Available, protocol.NewBlockWithHeight(95), methodsMock))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)
}

func TestChainSupervisorUnionUpstreamMethods(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods1 := mocks.NewMethodsMock()
	methods1.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))
	methods2 := mocks.NewMethodsMock()
	methods2.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test2"))
	methods3 := mocks.NewMethodsMock()
	methods3.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test2", "test5"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methods1))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id2", protocol.Available, protocol.NewBlockWithHeight(100), methods2))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Available, protocol.NewBlockWithHeight(100), methods3))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2", "test5"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods1))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test2", "test5"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())
}

func TestChainSupervisorUnionUpstreamBlockInfo(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockWithHeight(1000), protocol.FinalizedBlock)

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo1))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(1000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo1.AddBlock(protocol.NewBlockWithHeight(2000), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo1))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockWithHeight(500), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id1", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo2))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo3 := protocol.NewBlockInfo()
	blockInfo3.AddBlock(protocol.NewBlockWithHeight(50000), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id5", protocol.Available, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(50000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id5", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Unavailable, protocol.NewBlockWithHeight(100), methods, blockInfo3))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, uint64(500), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)
}

func TestChainSupervisorRemoveUpstreamState(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	time.Sleep(20 * time.Millisecond)

	assert.True(t, chainSupervisor.GetUpstreamState("id") != nil)

	chainSupervisor.Publish(test_utils.CreateRemoveEvent("id"))
	time.Sleep(20 * time.Millisecond)

	assert.Nil(t, chainSupervisor.GetUpstreamState("id"))
}

func TestChainSupervisorLowerBoundsInitialStateIsEmpty(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)

	assert.Empty(t, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsSingleAvailableUpstream(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	slotBound := protocol.NewLowerBoundData(120, 1000, protocol.SlotBound)
	stateBound := protocol.NewLowerBoundData(450, 1000, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("id", protocol.Available, 100, methods, slotBound, stateBound))
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.SlotBound:  slotBound,
		protocol.StateBound: stateBound,
	}, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsUseMinimumBoundPerTypeAcrossAvailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	slotBound1 := protocol.NewLowerBoundData(200, 1000, protocol.SlotBound)
	stateBound1 := protocol.NewLowerBoundData(500, 1000, protocol.StateBound)
	slotBound2 := protocol.NewLowerBoundData(150, 1010, protocol.SlotBound)
	stateBound2 := protocol.NewLowerBoundData(700, 1010, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("id1", protocol.Available, 100, methods, slotBound1, stateBound1))
	chainSupervisor.Publish(createEventWithLowerBounds("id2", protocol.Available, 110, methods, slotBound2, stateBound2))
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, slotBound2, chainSupervisor.GetChainState().LowerBounds[protocol.SlotBound])
	assert.Equal(t, stateBound1, chainSupervisor.GetChainState().LowerBounds[protocol.StateBound])
}

func TestChainSupervisorLowerBoundsIgnoreUnavailableUpstreams(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	availableBound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	unavailableBetterBound := protocol.NewLowerBoundData(100, 1010, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("available", protocol.Available, 100, methods, availableBound))
	chainSupervisor.Publish(createEventWithLowerBounds("unavailable", protocol.Unavailable, 100, methods, unavailableBetterBound))
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: availableBound,
	}, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsIgnoreUpstreamsWithoutLowerBoundsInfo(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	bound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	chainSupervisor.Publish(createEventWithLowerBounds("with-bounds", protocol.Available, 100, methods, bound))
	chainSupervisor.Publish(test_utils.CreateEvent("without-bounds", protocol.Available, protocol.NewBlockWithHeight(100), methods))
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: bound,
	}, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsUpdateExistingUpstreamState(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	initialBound := protocol.NewLowerBoundData(300, 1000, protocol.StateBound)
	updatedBound := protocol.NewLowerBoundData(200, 1010, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("id", protocol.Available, 100, methods, initialBound))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, initialBound, chainSupervisor.GetChainState().LowerBounds[protocol.StateBound])

	chainSupervisor.Publish(createEventWithLowerBounds("id", protocol.Available, 120, methods, updatedBound))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, updatedBound, chainSupervisor.GetChainState().LowerBounds[protocol.StateBound])
}

func TestChainSupervisorLowerBoundsRecomputeWhenUpstreamBecomesUnavailable(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	lowerBound := protocol.NewLowerBoundData(200, 1000, protocol.StateBound)
	higherBound := protocol.NewLowerBoundData(500, 1010, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("id1", protocol.Available, 100, methods, lowerBound))
	chainSupervisor.Publish(createEventWithLowerBounds("id2", protocol.Available, 100, methods, higherBound))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, lowerBound, chainSupervisor.GetChainState().LowerBounds[protocol.StateBound])

	chainSupervisor.Publish(createEventWithLowerBounds("id1", protocol.Unavailable, 100, methods, lowerBound))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: higherBound,
	}, chainSupervisor.GetChainState().LowerBounds)
}

func TestChainSupervisorLowerBoundsRecomputeWhenUpstreamRemoved(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	lowerBound := protocol.NewLowerBoundData(200, 1000, protocol.StateBound)
	higherBound := protocol.NewLowerBoundData(500, 1010, protocol.StateBound)

	chainSupervisor.Publish(createEventWithLowerBounds("id1", protocol.Available, 100, methods, lowerBound))
	chainSupervisor.Publish(createEventWithLowerBounds("id2", protocol.Available, 100, methods, higherBound))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, lowerBound, chainSupervisor.GetChainState().LowerBounds[protocol.StateBound])

	chainSupervisor.Publish(test_utils.CreateRemoveEvent("id1"))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, map[protocol.LowerBoundType]protocol.LowerBoundData{
		protocol.StateBound: higherBound,
	}, chainSupervisor.GetChainState().LowerBounds)

	chainSupervisor.Publish(test_utils.CreateRemoveEvent("id2"))
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, chainSupervisor.GetChainState().LowerBounds)
}
