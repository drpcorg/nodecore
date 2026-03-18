package event_processors_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBaseLowerBoundEventProcessorNilProcessorReturnsNil(t *testing.T) {
	processor := event_processors.NewBaseLowerBoundEventProcessor(context.Background(), "upstream-1", nil)

	assert.Nil(t, processor)
}

func TestBaseLowerBoundEventProcessorType(t *testing.T) {
	lowerBoundProcessor := mocks.NewLowerBoundProcessorMock()
	processor := event_processors.NewBaseLowerBoundEventProcessor(context.Background(), "upstream-1", lowerBoundProcessor)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.LowerBoundEventProcessorType, processor.Type())
}

func TestBaseLowerBoundEventProcessorRunningInitiallyFalse(t *testing.T) {
	lowerBoundProcessor := mocks.NewLowerBoundProcessorMock()
	processor := event_processors.NewBaseLowerBoundEventProcessor(context.Background(), "upstream-1", lowerBoundProcessor)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestBaseLowerBoundEventProcessorStartEmitsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lowerBoundProcessor := mocks.NewLowerBoundProcessorMock()
	processor := event_processors.NewBaseLowerBoundEventProcessor(ctx, "upstream-1", lowerBoundProcessor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 1)

	lowerBoundProcessor.On("Start").Return()
	lowerBoundProcessor.On("Subscribe", "upstream-1_lower_bounds")
	lowerBoundProcessor.On("Stop").Return()

	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	go processor.Start()

	data := protocol.NewLowerBoundData(123, 456, protocol.BlockBound)

	require.Eventually(t, func() bool {
		lowerBoundProcessor.Publish(data)

		select {
		case event := <-events:
			lowerBoundEvent, ok := event.(*protocol.LowerBoundUpstreamStateEvent)
			if !ok {
				return false
			}
			return lowerBoundEvent.Data == data
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	processor.Stop()
	lowerBoundProcessor.AssertExpectations(t)
}

func TestBaseLowerBoundEventProcessorStopStopsUnderlyingProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lowerBoundProcessor := mocks.NewLowerBoundProcessorMock()
	processor := event_processors.NewBaseLowerBoundEventProcessor(ctx, "upstream-1", lowerBoundProcessor)

	lowerBoundProcessor.On("Start").Return()
	lowerBoundProcessor.On("Subscribe", "upstream-1_lower_bounds")
	lowerBoundProcessor.On("Stop").Return()

	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

	go processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	processor.Stop()

	assert.False(t, processor.Running())
	lowerBoundProcessor.AssertExpectations(t)
}
