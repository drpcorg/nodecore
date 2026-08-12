package event_processors_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMethodsEventProcessorReturnsNilForNilProcessor(t *testing.T) {
	processor := event_processors.NewMethodsEventProcessor(context.Background(), "upstream-1", nil)

	assert.Nil(t, processor)
}

func TestMethodsEventProcessorType(t *testing.T) {
	methodsProcessor := mocks.NewMethodsProcessorMock()
	processor := event_processors.NewMethodsEventProcessor(context.Background(), "upstream-1", methodsProcessor)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.MethodsEventProcessorType, processor.Type())
}

func TestMethodsEventProcessorRunningInitiallyFalse(t *testing.T) {
	methodsProcessor := mocks.NewMethodsProcessorMock()
	processor := event_processors.NewMethodsEventProcessor(context.Background(), "upstream-1", methodsProcessor)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestMethodsEventProcessorStartEmitsUnsupportedMethods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	methodsProcessor := mocks.NewMethodsProcessorMock()
	methodsProcessor.On("Start").Return().Once()
	methodsProcessor.On("Subscribe", "upstream-1_methods").Return().Once()
	methodsProcessor.On("Stop").Return().Once()

	processor := event_processors.NewMethodsEventProcessor(ctx, "upstream-1", methodsProcessor)
	require.NotNil(t, processor)

	events := make(chan protocol.AbstractUpstreamStateEvent, 1)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	unsupported := mapset.NewThreadUnsafeSet[string]("trace_block")
	methodsProcessor.Publish(unsupported)

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			methodsEvent, ok := event.(*protocol.UnsupportedMethodsUpstreamStateEvent)
			return ok && unsupported.Equal(methodsEvent.Methods)
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	processor.Stop()
	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	methodsProcessor.AssertExpectations(t)
}

func TestMethodsEventProcessorStopStopsLifecycleAndDelegatesStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	methodsProcessor := mocks.NewMethodsProcessorMock()
	methodsProcessor.On("Start").Return().Once()
	methodsProcessor.On("Subscribe", "upstream-1_methods").Return().Once()
	methodsProcessor.On("Stop").Return().Once()

	processor := event_processors.NewMethodsEventProcessor(ctx, "upstream-1", methodsProcessor)
	require.NotNil(t, processor)
	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	methodsProcessor.AssertExpectations(t)
}
