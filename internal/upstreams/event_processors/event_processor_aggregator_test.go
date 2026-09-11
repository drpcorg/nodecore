package event_processors_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewUpstreamProcessorAggregator_SkipsNilProcessors(t *testing.T) {
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{
		nil,
		event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, mocks.NewHeadProcessorMock()),
	})

	assert.True(t, aggregator.IsHealthProcessorDisabled())
}

func TestUpstreamProcessorAggregatorIsHealthProcessorDisabled_FalseWhenPresent(t *testing.T) {
	validator := mocks.NewHealthValidatorMock()

	healthProcessor := event_processors.NewGenericHealthEventProcessor(
		context.Background(),
		"upstream-1",
		aggregatorTestUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{healthProcessor})

	assert.False(t, aggregator.IsHealthProcessorDisabled())
}

func TestUpstreamProcessorAggregatorUpdateHead_ForwardsData(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	headProcessor.On("UpdateHead", uint64(55), uint64(7)).Once()

	headEventProcessor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{headEventProcessor})

	aggregator.UpdateHead(event_processors.NewHeadUpdateData(55, 7))

	headProcessor.AssertExpectations(t)
}

func TestUpstreamProcessorAggregatorUpdateBlock_ForwardsData(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	blockData := protocol.NewBlockWithHeight(66)
	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	blockEventProcessor := event_processors.NewGenericBlockEventProcessor(context.Background(), "upstream-1", chains.ETHEREUM, blockProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{blockEventProcessor})

	aggregator.UpdateBlock(event_processors.NewGenericBlockUpdateData(blockData, protocol.FinalizedBlock))

	blockProcessor.AssertExpectations(t)
}

func TestUpstreamProcessorAggregatorValidateSettings_ReturnsProcessorResult(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.Valid).Once()

	settingsProcessor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		aggregatorTestUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{settingsProcessor})

	result, ok := aggregator.ValidateSettings()

	assert.True(t, ok)
	assert.Equal(t, validations.Valid, result)
	validator.AssertExpectations(t)
}

func TestUpstreamProcessorAggregatorValidateSettings_ReturnsUnknownWithoutSettingsProcessor(t *testing.T) {
	aggregator := event_processors.NewUpstreamProcessorAggregator(nil)

	result, ok := aggregator.ValidateSettings()

	assert.False(t, ok)
	assert.Equal(t, validations.UnknownResult, result)
}

func TestUpstreamProcessorAggregatorStartAndStopProcessor_ControlLifecycleByType(t *testing.T) {
	tests := []struct {
		name          string
		processorType event_processors.EventProcessorType
		processor     *mocks.UpstreamStateEventProcessorMock
	}{
		{
			name:          "block",
			processorType: event_processors.BlockEventProcessorType,
			processor:     mocks.NewUpstreamStateEventProcessorMock(event_processors.BlockEventProcessorType),
		},
		{
			name:          "head",
			processorType: event_processors.HeadEventProcessorType,
			processor:     mocks.NewUpstreamStateEventProcessorMock(event_processors.HeadEventProcessorType),
		},
		{
			name:          "lower-bound",
			processorType: event_processors.LowerBoundEventProcessorType,
			processor:     mocks.NewUpstreamStateEventProcessorMock(event_processors.LowerBoundEventProcessorType),
		},
		{
			name:          "health",
			processorType: event_processors.HealthValidatorProcessorType,
			processor:     mocks.NewUpstreamStateEventProcessorMock(event_processors.HealthValidatorProcessorType),
		},
		{
			name:          "settings",
			processorType: event_processors.SettingsValidatorProcessorType,
			processor:     mocks.NewUpstreamStateEventProcessorMock(event_processors.SettingsValidatorProcessorType),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := tt.processor
			processor.On("SetEmitter", mock.Anything).Once()
			processor.On("Start").Once()
			processor.On("Stop").Once()

			aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{processor})
			aggregator.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})

			aggregator.StartProcessor(tt.processorType)

			aggregator.StopProcessor(tt.processorType)

			time.Sleep(100 * time.Millisecond)
			processor.AssertExpectations(t)
		})
	}
}

func aggregatorTestUpstreamOptions() *chains.Options {
	return &chains.Options{
		InternalTimeout:             time.Second,
		ValidationInterval:          time.Second,
		DisableValidation:           new(false),
		DisableSettingsValidation:   new(false),
		DisableChainValidation:      new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(false),
	}
}

func TestUpstreamProcessorAggregatorSerializesConcurrentStartStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	headProcessor := mocks.NewHeadProcessorMock()
	headProcessor.On("Start").Return()
	headProcessor.On("Stop").Return()
	headProcessor.On("Subscribe", mock.Anything)

	processor := event_processors.NewHeadEventProcessor(ctx, "upstream-1", chains.ETHEREUM, headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{processor})
	events := make(chan protocol.AbstractUpstreamStateEvent, 100)
	aggregator.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	// the supervisor (Resume/PartialStop) and the upstream state loop (pause on syncing)
	// drive the same processor from different goroutines
	const iterations = 3000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iterations {
			aggregator.StartProcessor(event_processors.HeadEventProcessorType)
			aggregator.StopProcessor(event_processors.HeadEventProcessorType)
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			aggregator.StopProcessor(event_processors.HeadEventProcessorType)
			aggregator.StartProcessor(event_processors.HeadEventProcessorType)
		}
	}()
	wg.Wait()
	aggregator.StopProcessor(event_processors.HeadEventProcessorType)

	// everything is stopped: a head published now must reach no forwarder. A run that
	// escaped the lifecycle (started behind a concurrent stop) would still forward it.
	headProcessor.Publish(blocks.HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(1)})
	select {
	case event := <-events:
		t.Fatalf("an orphan forwarder is still alive: got %T after the final stop", event)
	case <-time.After(200 * time.Millisecond):
	}
}
