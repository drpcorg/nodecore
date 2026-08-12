package event_processors

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
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

type GenericSettingsEventProcessor struct {
	lifecycle          *utils.GenericLifecycle
	upstreamId         string
	validationInterval time.Duration
	validator          *validations.ValidationProcessor[validations.ValidationSettingResult]
	emitter            Emitter

	currentValidationState *utils.Atomic[validations.ValidationSettingResult]
}

func (b *GenericSettingsEventProcessor) Type() EventProcessorType {
	return SettingsValidatorProcessorType
}

func (b *GenericSettingsEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *GenericSettingsEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping settings validations events of upstream '%s'", b.upstreamId)
					return
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

func (b *GenericSettingsEventProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *GenericSettingsEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *GenericSettingsEventProcessor) Validate() validations.ValidationSettingResult {
	result := b.validator.Validate()
	b.currentValidationState.Store(result)

	return result
}

func NewGenericSettingsEventProcessor(
	ctx context.Context,
	upstreamId string,
	options *chains.Options,
	validator *validations.ValidationProcessor[validations.ValidationSettingResult],
) *GenericSettingsEventProcessor {
	if validator == nil || (*options.DisableValidation || *options.DisableSettingsValidation) {
		return nil
	}

	currentValidationState := utils.NewAtomic[validations.ValidationSettingResult]()
	currentValidationState.Store(validations.UnknownResult)

	return &GenericSettingsEventProcessor{
		lifecycle:              utils.NewGenericLifecycle(fmt.Sprintf("%s_settings_event_processor", upstreamId), ctx),
		validationInterval:     options.ValidationInterval,
		validator:              validator,
		upstreamId:             upstreamId,
		currentValidationState: currentValidationState,
	}
}

type HealthEventProcessor interface {
	ValidationEventProcessor[protocol.AvailabilityStatus]
}

type GenericHealthEventProcessor struct {
	lifecycle          *utils.GenericLifecycle
	emitter            Emitter
	upstreamId         string
	validationInterval time.Duration
	validator          *validations.ValidationProcessor[protocol.AvailabilityStatus]
}

func (b *GenericHealthEventProcessor) Type() EventProcessorType {
	return HealthValidatorProcessorType
}

func (b *GenericHealthEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *GenericHealthEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			b.validateHealth()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping health validations events of upstream '%s'", b.upstreamId)
					return
				case <-time.After(b.validationInterval):
					b.validateHealth()
				}
			}
		}()

		return nil
	})
}

func (b *GenericHealthEventProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *GenericHealthEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *GenericHealthEventProcessor) Validate() protocol.AvailabilityStatus {
	return b.validator.Validate()
}

func (b *GenericHealthEventProcessor) validateHealth() {
	availabilityStatus := b.Validate()
	log.Debug().Msgf("availability status of upstream '%s' - %s", b.upstreamId, availabilityStatus)

	b.emitter(&protocol.StatusUpstreamStateEvent{Status: availabilityStatus})
}

func NewGenericHealthEventProcessor(
	ctx context.Context,
	upstreamId string,
	options *chains.Options,
	validator *validations.ValidationProcessor[protocol.AvailabilityStatus],
) *GenericHealthEventProcessor {
	if validator == nil || (*options.DisableValidation || *options.DisableHealthValidation) {
		return nil
	}

	return &GenericHealthEventProcessor{
		lifecycle:          utils.NewGenericLifecycle(fmt.Sprintf("%s_health_event_processor", upstreamId), ctx),
		validationInterval: options.ValidationInterval,
		validator:          validator,
		upstreamId:         upstreamId,
	}
}

var _ HealthEventProcessor = (*GenericHealthEventProcessor)(nil)
var _ SettingsEventProcessor = (*GenericSettingsEventProcessor)(nil)
