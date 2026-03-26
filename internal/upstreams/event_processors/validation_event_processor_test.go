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
	"github.com/stretchr/testify/require"
)

func TestNewBaseSettingsEventProcessorReturnsNilForNilValidator(t *testing.T) {
	processor := event_processors.NewBaseSettingsEventProcessor(context.Background(), "upstream-1", testUpstreamOptions(), nil)

	assert.Nil(t, processor)
}

func TestNewBaseSettingsEventProcessorReturnsNilWhenValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableValidation = true
	validator := validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{mocks.NewSettingsValidatorMock()})

	processor := event_processors.NewBaseSettingsEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestNewBaseSettingsEventProcessorReturnsNilWhenSettingsValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableSettingsValidation = true
	validator := validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{mocks.NewSettingsValidatorMock()})

	processor := event_processors.NewBaseSettingsEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestBaseSettingsEventProcessorType(t *testing.T) {
	validatorMock := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewBaseSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.SettingsValidatorProcessorType, processor.Type())
}

func TestBaseSettingsEventProcessorRunningInitiallyFalse(t *testing.T) {
	validatorMock := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewBaseSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestBaseSettingsEventProcessorValidateReturnsReducedResult(t *testing.T) {
	validator1 := mocks.NewSettingsValidatorMock()
	validator2 := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewBaseSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator1, validator2}),
	)

	require.NotNil(t, processor)
	validator1.On("Validate").Return(validations.Valid).Once()
	validator2.On("Validate").Return(validations.FatalSettingError).Once()

	assert.Equal(t, validations.FatalSettingError, processor.Validate())
	validator1.AssertExpectations(t)
	validator2.AssertExpectations(t)
}

func TestBaseSettingsEventProcessorStartFollowsCurrentValidationStateTransitions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewBaseSettingsEventProcessor(
		ctx,
		"upstream-1",
		testUpstreamOptionsWithInterval(20*time.Millisecond),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)

	require.NotNil(t, processor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 1)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	validator.On("Validate").Return(validations.SettingsError).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Once()
	validator.On("Validate").Return(validations.Valid).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Maybe()

	processor.Start()
	defer processor.Stop()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	select {
	case event := <-events:
		t.Fatalf("unexpected event after first settings validation tick: %T", event)
	default:
	}

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			_, ok := event.(*protocol.FatalErrorUpstreamStateEvent)
			return ok
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond)

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			_, ok := event.(*protocol.ValidUpstreamStateEvent)
			return ok
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond)

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			_, ok := event.(*protocol.FatalErrorUpstreamStateEvent)
			return ok
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond)

	validator.AssertExpectations(t)
}

func TestBaseSettingsEventProcessorStartDoesNotEmitEventFromUnknownState(t *testing.T) {
	tests := []struct {
		name   string
		result validations.ValidationSettingResult
	}{
		{
			"FatalError",
			validations.FatalSettingError,
		},
		{
			"Valid",
			validations.Valid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			validator := mocks.NewSettingsValidatorMock()
			processor := event_processors.NewBaseSettingsEventProcessor(
				ctx,
				"upstream-1",
				testUpstreamOptions(),
				validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
			)

			require.NotNil(t, processor)
			events := make(chan protocol.AbstractUpstreamStateEvent, 1)
			processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
				events <- event
			})

			validator.On("Validate").Return(tt.result).Once()
			validator.On("Validate").Return(tt.result).Maybe()

			processor.Start()
			defer processor.Stop()

			require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

			assert.Never(t, func() bool {
				select {
				case <-events:
					return true
				default:
					return false
				}
			}, 100*time.Millisecond, 10*time.Millisecond)

			validator.AssertExpectations(t)
		})
	}
}

func TestBaseSettingsEventProcessorStartEmitsFatalErrorAfterValidState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewBaseSettingsEventProcessor(
		ctx,
		"upstream-1",
		testUpstreamOptionsWithInterval(20*time.Millisecond),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)

	require.NotNil(t, processor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 1)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	validator.On("Validate").Return(validations.Valid).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Maybe()

	processor.Start()
	defer processor.Stop()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	select {
	case event := <-events:
		t.Fatalf("unexpected event after first valid settings validation tick: %T", event)
	default:
	}

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			_, ok := event.(*protocol.FatalErrorUpstreamStateEvent)
			return ok
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond)

	validator.AssertExpectations(t)
}

func TestNewBaseHealthEventProcessorReturnsNilForNilValidator(t *testing.T) {
	processor := event_processors.NewBaseHealthEventProcessor(context.Background(), "upstream-1", testUpstreamOptions(), nil)

	assert.Nil(t, processor)
}

func TestNewBaseHealthEventProcessorReturnsNilWhenValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableValidation = true
	validator := validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{mocks.NewHealthValidatorMock()})

	processor := event_processors.NewBaseHealthEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestNewBaseHealthEventProcessorReturnsNilWhenHealthValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableHealthValidation = true
	validator := validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{mocks.NewHealthValidatorMock()})

	processor := event_processors.NewBaseHealthEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestBaseHealthEventProcessorType(t *testing.T) {
	validatorMock := mocks.NewHealthValidatorMock()
	processor := event_processors.NewBaseHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.HealthValidatorProcessorType, processor.Type())
}

func TestBaseHealthEventProcessorRunningInitiallyFalse(t *testing.T) {
	validatorMock := mocks.NewHealthValidatorMock()
	processor := event_processors.NewBaseHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestBaseHealthEventProcessorValidateReturnsReducedStatus(t *testing.T) {
	validator1 := mocks.NewHealthValidatorMock()
	validator2 := mocks.NewHealthValidatorMock()
	processor := event_processors.NewBaseHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator1, validator2}),
	)

	require.NotNil(t, processor)
	validator1.On("Validate").Return(protocol.Available).Once()
	validator2.On("Validate").Return(protocol.Unavailable).Once()

	assert.Equal(t, protocol.Unavailable, processor.Validate())
	validator1.AssertExpectations(t)
	validator2.AssertExpectations(t)
}

func TestBaseHealthEventProcessorStartEmitsStatusEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewHealthValidatorMock()
	options := testUpstreamOptions()
	options.ValidationInterval = 50 * time.Millisecond
	processor := event_processors.NewBaseHealthEventProcessor(
		ctx,
		"upstream-1",
		options,
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator}),
	)

	require.NotNil(t, processor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 2)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	validator.On("Validate").Return(protocol.Syncing).Once()

	processor.Start()
	defer processor.Stop()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			statusEvent, ok := event.(*protocol.StatusUpstreamStateEvent)
			return ok && statusEvent.Status == protocol.Syncing
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	validator.AssertExpectations(t)
}

func TestBaseHealthEventProcessorStopStopsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewHealthValidatorMock()
	options := testUpstreamOptions()
	options.ValidationInterval = 50 * time.Millisecond
	processor := event_processors.NewBaseHealthEventProcessor(
		ctx,
		"upstream-1",
		options,
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator}),
	)

	require.NotNil(t, processor)
	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})
	validator.On("Validate").Return(protocol.Available).Maybe()

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)

	validator.AssertExpectations(t)
}

func testUpstreamOptions() *config.UpstreamOptions {
	return testUpstreamOptionsWithInterval(10 * time.Millisecond)
}

func testUpstreamOptionsWithInterval(interval time.Duration) *config.UpstreamOptions {
	return &config.UpstreamOptions{
		ValidationInterval:        interval,
		DisableValidation:         new(false),
		DisableSettingsValidation: new(false),
		DisableHealthValidation:   new(false),
	}
}
