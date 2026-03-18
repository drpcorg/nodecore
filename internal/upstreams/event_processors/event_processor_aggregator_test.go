package event_processors_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewUpstreamProcessorAggregator_SkipsNilProcessors(t *testing.T) {
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{
		nil,
		event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", mocks.NewHeadProcessorMock()),
	})

	assert.True(t, aggregator.IsHealthProcessorDisabled())
}

func TestUpstreamProcessorAggregatorIsHealthProcessorDisabled_FalseWhenPresent(t *testing.T) {
	validator := mocks.NewHealthValidatorMock()

	healthProcessor := event_processors.NewBaseHealthEventProcessor(
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

	headEventProcessor := event_processors.NewHeadEventProcessor(context.Background(), "upstream-1", headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{headEventProcessor})

	aggregator.UpdateHead(event_processors.NewHeadUpdateData(55, 7))

	headProcessor.AssertExpectations(t)
}

func TestUpstreamProcessorAggregatorUpdateBlock_ForwardsData(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	blockData := protocol.NewBlockDataWithHeight(66)
	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	blockEventProcessor := event_processors.NewBaseBlockEventProcessor(context.Background(), "upstream-1", blockProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{blockEventProcessor})

	aggregator.UpdateBlock(event_processors.NewBaseBlockUpdateData(blockData, protocol.FinalizedBlock))

	blockProcessor.AssertExpectations(t)
}

func TestUpstreamProcessorAggregatorValidateSettings_ReturnsProcessorResult(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.Valid).Once()

	settingsProcessor := event_processors.NewBaseSettingsEventProcessor(
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

func aggregatorTestUpstreamOptions() *config.UpstreamOptions {
	return &config.UpstreamOptions{
		InternalTimeout:             time.Second,
		ValidationInterval:          time.Second,
		DisableValidation:           new(false),
		DisableSettingsValidation:   new(false),
		DisableChainValidation:      new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(false),
	}
}
