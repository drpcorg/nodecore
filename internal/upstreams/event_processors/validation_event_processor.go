package event_processors

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type ValidationEventProcessor[R any] interface {
	UpstreamStateEventProcessor

	Validate() R
}

type SettingsEventProcessor interface {
	ValidationEventProcessor[validations.ValidationSettingResult]
}

type BaseSettingsEventProcessor struct {
	lifecycle          *utils.BaseLifecycle
	upstreamId         string
	validationInterval time.Duration
	validator          *validations.ValidationProcessor[validations.ValidationSettingResult]
	emitter            Emitter

	currentValidationState *utils.Atomic[validations.ValidationSettingResult]
}

func (b *BaseSettingsEventProcessor) Type() EventProcessorType {
	return SettingsValidatorProcessorType
}

func (b *BaseSettingsEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *BaseSettingsEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping settings validations events of upstream '%s'", b.upstreamId)
				case <-time.After(b.validationInterval):
					currentValidationState := b.currentValidationState.Load()
					validationResult := b.Validate()
					switch validationResult {
					case validations.FatalSettingError:
						if currentValidationState == validations.SettingsError || currentValidationState == validations.Valid {
							b.emitter(&protocol.FatalErrorUpstreamStateEvent{})
						}
					case validations.SettingsError:
						log.Debug().Msg("keep validating...")
					case validations.Valid:
						if currentValidationState == validations.SettingsError || currentValidationState == validations.FatalSettingError {
							b.emitter(&protocol.ValidUpstreamStateEvent{})
						}
					case validations.UnknownResult:
						// skip
					}
				}
			}
		}()
		return nil
	})
}

func (b *BaseSettingsEventProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *BaseSettingsEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *BaseSettingsEventProcessor) Validate() validations.ValidationSettingResult {
	result := b.validator.Validate()
	b.currentValidationState.Store(result)

	return result
}

func NewBaseSettingsEventProcessor(
	ctx context.Context,
	upstreamId string,
	options *config.UpstreamOptions,
	validator *validations.ValidationProcessor[validations.ValidationSettingResult],
) *BaseSettingsEventProcessor {
	if validator == nil || (*options.DisableValidation || *options.DisableSettingsValidation) {
		return nil
	}

	currentValidationState := utils.NewAtomic[validations.ValidationSettingResult]()
	currentValidationState.Store(validations.UnknownResult)

	return &BaseSettingsEventProcessor{
		lifecycle:              utils.NewBaseLifecycle(fmt.Sprintf("%s_settings_event_processor", upstreamId), ctx),
		validationInterval:     options.ValidationInterval,
		validator:              validator,
		upstreamId:             upstreamId,
		currentValidationState: currentValidationState,
	}
}

type HealthEventProcessor interface {
	ValidationEventProcessor[protocol.AvailabilityStatus]
}

type BaseHealthEventProcessor struct {
	lifecycle          *utils.BaseLifecycle
	emitter            Emitter
	upstreamId         string
	validationInterval time.Duration
	validator          *validations.ValidationProcessor[protocol.AvailabilityStatus]
}

func (b *BaseHealthEventProcessor) Type() EventProcessorType {
	return HealthValidatorProcessorType
}

func (b *BaseHealthEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *BaseHealthEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			b.validateHealth()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping health validations events of upstream '%s'", b.upstreamId)
				case <-time.After(b.validationInterval):
					b.validateHealth()
				}
			}
		}()

		return nil
	})
}

func (b *BaseHealthEventProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *BaseHealthEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *BaseHealthEventProcessor) Validate() protocol.AvailabilityStatus {
	return b.validator.Validate()
}

func (b *BaseHealthEventProcessor) validateHealth() {
	availabilityStatus := b.Validate()
	log.Debug().Msgf("availability status of upstream '%s' - %s", b.upstreamId, availabilityStatus)

	b.emitter(&protocol.StatusUpstreamStateEvent{Status: availabilityStatus})
}

func NewBaseHealthEventProcessor(
	ctx context.Context,
	upstreamId string,
	options *config.UpstreamOptions,
	validator *validations.ValidationProcessor[protocol.AvailabilityStatus],
) *BaseHealthEventProcessor {
	if validator == nil || (*options.DisableValidation || *options.DisableHealthValidation) {
		return nil
	}

	return &BaseHealthEventProcessor{
		lifecycle:          utils.NewBaseLifecycle(fmt.Sprintf("%s_health_event_processor", upstreamId), ctx),
		validationInterval: options.ValidationInterval,
		validator:          validator,
		upstreamId:         upstreamId,
	}
}

var _ HealthEventProcessor = (*BaseHealthEventProcessor)(nil)
var _ SettingsEventProcessor = (*BaseSettingsEventProcessor)(nil)
