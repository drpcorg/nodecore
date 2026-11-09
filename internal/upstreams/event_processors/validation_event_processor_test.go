package event_processors_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewGenericSettingsEventProcessorReturnsNilForNilValidator(t *testing.T) {
	processor := event_processors.NewGenericSettingsEventProcessor(context.Background(), "upstream-1", testUpstreamOptions(), nil)

	assert.Nil(t, processor)
}

func TestNewGenericSettingsEventProcessorReturnsNilWhenValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableValidation = true
	validator := validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{mocks.NewSettingsValidatorMock()})

	processor := event_processors.NewGenericSettingsEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestNewGenericSettingsEventProcessorReturnsNilWhenSettingsValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableSettingsValidation = true
	validator := validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{mocks.NewSettingsValidatorMock()})

	processor := event_processors.NewGenericSettingsEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestGenericSettingsEventProcessorType(t *testing.T) {
	validatorMock := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.SettingsValidatorProcessorType, processor.Type())
}

func TestGenericSettingsEventProcessorRunningInitiallyFalse(t *testing.T) {
	validatorMock := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestGenericSettingsEventProcessorValidateReturnsReducedResult(t *testing.T) {
	validator1 := mocks.NewSettingsValidatorMock()
	validator2 := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator1, validator2}),
	)

	require.NotNil(t, processor)
	validator1.On("Validate", mock.Anything).Return(validations.Valid).Once()
	validator2.On("Validate", mock.Anything).Return(validations.FatalSettingError).Once()

	assert.Equal(t, validations.FatalSettingError, processor.Validate(context.Background()))
	validator1.AssertExpectations(t)
	validator2.AssertExpectations(t)
}

// A repeated verdict is emitted again: the upstream owns "is it valid" and dedupes, and a
// Valid after a head-driven removal must be able to re-admit the upstream.
func TestGenericSettingsEventProcessorEmitsTheVerdictOnEveryTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewGenericSettingsEventProcessor(
		ctx,
		"upstream-1",
		testUpstreamOptionsWithInterval(20*time.Millisecond),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)

	require.NotNil(t, processor)
	events := make(chan protocol.AbstractUpstreamStateEvent, 10)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})

	validator.On("Validate", mock.Anything).Return(validations.Valid).Once()
	validator.On("Validate", mock.Anything).Return(validations.Valid).Once()
	validator.On("Validate", mock.Anything).Return(validations.FatalSettingError).Once()
	validator.On("Validate", mock.Anything).Return(validations.FatalSettingError).Once()
	validator.On("Validate", mock.Anything).Return(validations.Valid).Maybe()

	processor.Start()
	defer processor.Stop()

	expected := []protocol.AbstractUpstreamStateEvent{
		&protocol.ValidUpstreamStateEvent{},
		&protocol.ValidUpstreamStateEvent{},
		&protocol.FatalErrorUpstreamStateEvent{},
		&protocol.FatalErrorUpstreamStateEvent{},
		&protocol.ValidUpstreamStateEvent{},
	}
	for i, want := range expected {
		select {
		case event := <-events:
			assert.IsType(t, want, event, "event %d", i)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}

	validator.AssertExpectations(t)
}

func TestGenericSettingsEventProcessorEmitsNothingOnSettingsErrorOrUnknown(t *testing.T) {
	tests := []struct {
		name   string
		result validations.ValidationSettingResult
	}{
		{
			"SettingsError",
			validations.SettingsError,
		},
		{
			"Unknown",
			validations.UnknownResult,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			validator := mocks.NewSettingsValidatorMock()
			processor := event_processors.NewGenericSettingsEventProcessor(
				ctx,
				"upstream-1",
				testUpstreamOptionsWithInterval(20*time.Millisecond),
				validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
			)

			require.NotNil(te, processor)
			events := make(chan protocol.AbstractUpstreamStateEvent, 10)
			processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
				events <- event
			})

			validator.On("Validate", mock.Anything).Return(tt.result).Once()
			validator.On("Validate", mock.Anything).Return(tt.result).Maybe()

			processor.Start()
			defer processor.Stop()

			require.Eventually(te, processor.Running, time.Second, 10*time.Millisecond)

			// several ticks pass (20ms interval) and none of them emits
			assert.Never(te, func() bool {
				select {
				case <-events:
					return true
				default:
					return false
				}
			}, 100*time.Millisecond, 10*time.Millisecond)

			validator.AssertExpectations(te)
		})
	}
}

func TestNewGenericHealthEventProcessorReturnsNilForNilValidator(t *testing.T) {
	processor := event_processors.NewGenericHealthEventProcessor(context.Background(), "upstream-1", testUpstreamOptions(), nil)

	assert.Nil(t, processor)
}

func TestNewGenericHealthEventProcessorReturnsNilWhenValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableValidation = true
	validator := validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{mocks.NewHealthValidatorMock()})

	processor := event_processors.NewGenericHealthEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestNewGenericHealthEventProcessorReturnsNilWhenHealthValidationDisabled(t *testing.T) {
	options := testUpstreamOptions()
	*options.DisableHealthValidation = true
	validator := validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{mocks.NewHealthValidatorMock()})

	processor := event_processors.NewGenericHealthEventProcessor(context.Background(), "upstream-1", options, validator)

	assert.Nil(t, processor)
}

func TestGenericHealthEventProcessorType(t *testing.T) {
	validatorMock := mocks.NewHealthValidatorMock()
	processor := event_processors.NewGenericHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.Equal(t, event_processors.HealthValidatorProcessorType, processor.Type())
}

func TestGenericHealthEventProcessorRunningInitiallyFalse(t *testing.T) {
	validatorMock := mocks.NewHealthValidatorMock()
	processor := event_processors.NewGenericHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validatorMock}),
	)

	require.NotNil(t, processor)
	assert.False(t, processor.Running())
}

func TestGenericHealthEventProcessorValidateReturnsReducedStatus(t *testing.T) {
	validator1 := mocks.NewHealthValidatorMock()
	validator2 := mocks.NewHealthValidatorMock()
	processor := event_processors.NewGenericHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator1, validator2}),
	)

	require.NotNil(t, processor)
	validator1.On("Validate", mock.Anything).Return(protocol.Available).Once()
	validator2.On("Validate", mock.Anything).Return(protocol.Unavailable).Once()

	assert.Equal(t, protocol.Unavailable, processor.Validate(context.Background()))
	validator1.AssertExpectations(t)
	validator2.AssertExpectations(t)
}

func TestGenericHealthEventProcessorStartEmitsStatusEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewHealthValidatorMock()
	options := testUpstreamOptions()
	options.ValidationInterval = 50 * time.Millisecond
	processor := event_processors.NewGenericHealthEventProcessor(
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

	validator.On("Validate", mock.Anything).Return(protocol.Syncing).Once()

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

func TestGenericHealthEventProcessorStopStopsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	validator := mocks.NewHealthValidatorMock()
	options := testUpstreamOptions()
	options.ValidationInterval = 50 * time.Millisecond
	processor := event_processors.NewGenericHealthEventProcessor(
		ctx,
		"upstream-1",
		options,
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator}),
	)

	require.NotNil(t, processor)
	processor.SetEmitter(func(protocol.AbstractUpstreamStateEvent) {})
	validator.On("Validate", mock.Anything).Return(protocol.Available).Maybe()

	processor.Start()

	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)

	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)

	validator.AssertExpectations(t)
}

func testUpstreamOptions() *chains.Options {
	return testUpstreamOptionsWithInterval(10 * time.Millisecond)
}

func testUpstreamOptionsWithInterval(interval time.Duration) *chains.Options {
	return &chains.Options{
		ValidationInterval:        interval,
		DisableValidation:         new(false),
		DisableSettingsValidation: new(false),
		DisableHealthValidation:   new(false),
	}
}

func TestGenericHealthEventProcessorDoesNotEmitAVerdictOfAValidationCancelledByStop(t *testing.T) {
	validator := mocks.NewHealthValidatorMock()
	processor := event_processors.NewGenericHealthEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewHealthValidationProcessor([]validations.Validator[protocol.AvailabilityStatus]{validator}),
	)
	require.NotNil(t, processor)

	events := make(chan protocol.AbstractUpstreamStateEvent, 10)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})
	validating := make(chan struct{}, 10)
	// the probe answers only once its context is cancelled, like a connector call does
	validator.On("Validate", mock.Anything).Run(func(args mock.Arguments) {
		validating <- struct{}{}
		<-args.Get(0).(context.Context).Done()
	}).Return(protocol.Unavailable)

	processor.Start()
	<-validating
	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("unexpected event %T after stop", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGenericSettingsEventProcessorDoesNotEmitAVerdictOfAValidationCancelledByStop(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	processor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"upstream-1",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	require.NotNil(t, processor)

	events := make(chan protocol.AbstractUpstreamStateEvent, 10)
	processor.SetEmitter(func(event protocol.AbstractUpstreamStateEvent) {
		events <- event
	})
	validating := make(chan struct{}, 10)
	validator.On("Validate", mock.Anything).Run(func(args mock.Arguments) {
		validating <- struct{}{}
		<-args.Get(0).(context.Context).Done()
	}).Return(validations.Valid)

	processor.Start()
	<-validating
	processor.Stop()

	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("unexpected event %T after stop", event)
	case <-time.After(100 * time.Millisecond):
	}
}
