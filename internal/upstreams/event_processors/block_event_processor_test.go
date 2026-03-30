package event_processors_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewBaseBlockEventProcessorNilProcessorReturnsNil(t *testing.T) {
	processor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, nil)

	assert.Nil(t, processor)
}

func TestNewHeadEventProcessorNilProcessorReturnsNil(t *testing.T) {
	processor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, nil)

	assert.Nil(t, processor)
}

func TestBaseBlockEventProcessorType(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, blockProcessor)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.BlockEventProcessorType, processor.Type())
}

func TestHeadEventProcessorType(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, headProcessor)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.HeadEventProcessorType, processor.Type())
}

func TestBaseBlockEventProcessorRunningInitiallyFalse(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, blockProcessor)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestHeadEventProcessorRunningInitiallyFalse(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, headProcessor)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestBaseBlockEventProcessorUpdateBlockForwardsData(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, blockProcessor)
	blockData := protocol.NewBlockWithHeight(42)

	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	processor.UpdateBlock(event_processors.NewBaseBlockUpdateData(blockData, protocol.FinalizedBlock))

	blockProcessor.AssertExpectations(t)
}

func TestBaseBlockEventProcessorUpdateBlockIgnoresUnsupportedData(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, blockProcessor)

	processor.UpdateBlock(event_processors.NewHeadUpdateData(12, 34))

	blockProcessor.AssertNotCalled(t, "UpdateBlock", mock.Anything, mock.Anything)
}

func TestHeadEventProcessorUpdateBlockForwardsData(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, headProcessor)

	headProcessor.On("UpdateHead", uint64(12), uint64(34)).Once()

	processor.UpdateBlock(event_processors.NewHeadUpdateData(12, 34))

	headProcessor.AssertExpectations(t)
}

func TestHeadEventProcessorUpdateBlockIgnoresUnsupportedData(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, headProcessor)

	processor.UpdateBlock(event_processors.NewBaseBlockUpdateData(protocol.NewBlockWithHeight(55), protocol.FinalizedBlock))

	headProcessor.AssertNotCalled(t, "UpdateHead", mock.Anything, mock.Anything)
}

func TestBaseBlockEventProcessorStartEmitsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(ctx, "upstream-1", chains.ETHEREUM, blockProcessor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 1)

	blockProcessor.On("Start").Return()
	blockProcessor.On("Subscribe", "upstream-1_block_updates")
	blockProcessor.On("Stop").Return()

	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	processor.Start()

	blockData := protocol.NewBlockWithHeight(101)

	require.Eventually(t, func() bool {
		blockProcessor.Publish(blocks.BlockEvent{Block: blockData, BlockType: protocol.FinalizedBlock})

		select {
		case event := <-events:
			blockEvent, ok := event.(*protocol.BlockUpstreamStateEvent)
			if !ok {
				return false
			}
			return blockEvent.Block.Equals(blockData) && blockEvent.BlockType == protocol.FinalizedBlock
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	processor.Stop()
	blockProcessor.AssertExpectations(t)
}

func TestHeadEventProcessorStartEmitsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(ctx, "upstream-1", chains.ETHEREUM, headProcessor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 1)

	headProcessor.On("Start").Return()
	headProcessor.On("Subscribe", "upstream-1_head_updates")
	headProcessor.On("Stop").Return()

	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	processor.Start()

	headData := protocol.NewBlockWithHeight(202)

	require.Eventually(t, func() bool {
		headProcessor.Publish(blocks.HeadEvent{HeadData: headData})

		select {
		case event := <-events:
			headEvent, ok := event.(*protocol.HeadUpstreamStateEvent)
			if !ok {
				return false
			}
			return headEvent.HeadData.Equals(headData)
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	processor.Stop()
	headProcessor.AssertExpectations(t)
}

func TestBaseBlockEventProcessorStopStopsUnderlyingProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blockProcessor := mocks.NewBlockProcessorMock()
	processor := event_processors.NewBaseBlockEventProcessor(ctx, "upstream-1", chains.ETHEREUM, blockProcessor)

	blockProcessor.On("Start").Return()
	blockProcessor.On("Subscribe", "upstream-1_block_updates")
	blockProcessor.On("Stop").Return()

	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	processor.Stop()

	assert.False(t, processor.Running())
	blockProcessor.AssertExpectations(t)
}

func TestHeadEventProcessorStopStopsUnderlyingProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	headProcessor := mocks.NewHeadProcessorMock()
	processor := event_processors.NewHeadEventProcessor(ctx, "upstream-1", chains.ETHEREUM, headProcessor)

	headProcessor.On("Start").Return()
	headProcessor.On("Subscribe", "upstream-1_head_updates")
	headProcessor.On("Stop").Return()

	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	processor.Stop()

	assert.False(t, processor.Running())
	headProcessor.AssertExpectations(t)
}
