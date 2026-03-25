package event_processors_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLabelsEventProcessorReturnsNilForNilProcessor(t *testing.T) {
	processor := event_processors.NewLabelsEventProcessor(context.Background(), "upstream-1", nil)

	assert.Nil(t, processor)
}

func TestLabelsEventProcessorType(t *testing.T) {
	labelsProcessor := mocks.NewLabelsProcessorMock()
	processor := event_processors.NewLabelsEventProcessor(context.Background(), "upstream-1", labelsProcessor)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.LabelsProcessorType, processor.Type())
}

func TestLabelsEventProcessorRunningInitiallyFalse(t *testing.T) {
	labelsProcessor := mocks.NewLabelsProcessorMock()
	processor := event_processors.NewLabelsEventProcessor(context.Background(), "upstream-1", labelsProcessor)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestLabelsEventProcessorStartEmitsLabelEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	labelsProcessor := mocks.NewLabelsProcessorMock()
	labelsProcessor.On("Start").Return().Once()
	labelsProcessor.On("Subscribe", "upstream-1_labels").Return().Once()
	labelsProcessor.On("Stop").Return().Once()

	processor := event_processors.NewLabelsEventProcessor(ctx, "upstream-1", labelsProcessor)
	require.NotNil(t, processor)

	events := make(chan protocol.AbstractUpstreamStateEvent, 1)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	go processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	labelsProcessor.Publish(lo.T2("client_type", "solana"))

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			labelsEvent, ok := event.(*protocol.LabelsUpstreamStateEvent)
			return ok && labelsEvent.Labels == lo.T2("client_type", "solana")
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	processor.Stop()
	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	labelsProcessor.AssertExpectations(t)
}

func TestLabelsEventProcessorStopStopsLifecycleAndDelegatesStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	labelsProcessor := mocks.NewLabelsProcessorMock()
	labelsProcessor.On("Start").Return().Once()
	labelsProcessor.On("Subscribe", "upstream-1_labels").Return().Once()
	labelsProcessor.On("Stop").Return().Once()

	processor := event_processors.NewLabelsEventProcessor(ctx, "upstream-1", labelsProcessor)
	require.NotNil(t, processor)
	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

	go processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	labelsProcessor.AssertExpectations(t)
}
