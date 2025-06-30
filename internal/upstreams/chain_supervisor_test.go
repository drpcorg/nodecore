package upstreams_test

import (
	"context"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestChainSupervisorUpdateHeadWithHeightFc(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Available, 95, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(test_utils.CreateEvent("id3", protocol.Unavailable, 500, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 1000, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(1000), chainSupervisor.GetChainState().Head)
}

func TestChainSupervisorTrackLags(t *testing.T) {
	tracker := dimensions.NewDimensionTracker()
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), tracker)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(600), protocol.FinalizedBlock)
	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockDataWithHeight(700), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id1", protocol.Available, 100, methodsMock, blockInfo1))
	time.Sleep(3 * time.Millisecond)

	chainDims1 := tracker.GetChainDimensions(chains.ARBITRUM, "id1")
	assert.Equal(t, uint64(0), chainDims1.GetHeadLag())
	assert.Equal(t, uint64(0), chainDims1.GetFinalizationLag())

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id2", protocol.Available, 300, methodsMock, blockInfo2))
	time.Sleep(3 * time.Millisecond)

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

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Unavailable, 95, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Unavailable, 500, methodsMock))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Unavailable, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(test_utils.CreateEvent("id12", protocol.Available, 95, methodsMock))
	time.Sleep(3 * time.Millisecond)
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

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, methods1))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id2", protocol.Available, 100, methods2))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id1", protocol.Available, 100, methods3))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2", "test5"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Unavailable, 100, methods1))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test2", "test5"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())
}

func TestChainSupervisorUnionUpstreamBlockInfo(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("test1"))

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(1000), protocol.FinalizedBlock)

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, 100, methods, blockInfo1))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(1000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(2000), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, 100, methods, blockInfo1))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo2 := protocol.NewBlockInfo()
	blockInfo2.AddBlock(protocol.NewBlockDataWithHeight(500), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id1", protocol.Available, 100, methods, blockInfo2))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	blockInfo3 := protocol.NewBlockInfo()
	blockInfo3.AddBlock(protocol.NewBlockDataWithHeight(50000), protocol.FinalizedBlock)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id5", protocol.Available, 100, methods, blockInfo3))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(50000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id5", protocol.Unavailable, 100, methods, blockInfo3))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(2000), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Unavailable, 100, methods, blockInfo3))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(500), chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock].Height)
}
