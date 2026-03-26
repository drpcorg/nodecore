package event_processors

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type EventProcessorType int

const (
	BlockEventProcessorType EventProcessorType = iota
	HeadEventProcessorType
	LowerBoundEventProcessorType
	HealthValidatorProcessorType
	SettingsValidatorProcessorType
	LabelsProcessorType
)

type UpstreamStateEventProcessor interface {
	utils.Lifecycle

	SetEmitter(emitter Emitter)
	Type() EventProcessorType
}

type Emitter func(event protocol.AbstractUpstreamStateEvent)

type UpstreamProcessorAggregator struct {
	eventProcessors map[EventProcessorType]UpstreamStateEventProcessor
}

func (u *UpstreamProcessorAggregator) SetEmitter(emitter Emitter) {
	for _, eventProcessor := range u.eventProcessors {
		eventProcessor.SetEmitter(emitter)
	}
}

func (u *UpstreamProcessorAggregator) UpdateHead(data BlockUpdateData) {
	if processor, ok := u.eventProcessors[HeadEventProcessorType]; ok {
		if headProcessor, ok := processor.(BlockEventProcessor); ok {
			headProcessor.UpdateBlock(data)
		}
	}
}

func (u *UpstreamProcessorAggregator) UpdateBlock(data BlockUpdateData) {
	if processor, ok := u.eventProcessors[BlockEventProcessorType]; ok {
		if blockProcessor, ok := processor.(BlockEventProcessor); ok {
			blockProcessor.UpdateBlock(data)
		}
	}
}

func (u *UpstreamProcessorAggregator) ValidateSettings() (validations.ValidationSettingResult, bool) {
	if processor, ok := u.eventProcessors[SettingsValidatorProcessorType]; ok {
		if settingsProcessor, ok := processor.(SettingsEventProcessor); ok {
			return settingsProcessor.Validate(), true
		}
	}
	return validations.UnknownResult, false
}

func (u *UpstreamProcessorAggregator) StartProcessor(processorType EventProcessorType) {
	if processor, ok := u.eventProcessors[processorType]; ok {
		processor.Start()
	}
}

func (u *UpstreamProcessorAggregator) StopProcessor(processorType EventProcessorType) {
	if processor, ok := u.eventProcessors[processorType]; ok {
		processor.Stop()
	}
}

func (u *UpstreamProcessorAggregator) IsHealthProcessorDisabled() bool {
	_, ok := u.eventProcessors[HealthValidatorProcessorType]
	return !ok
}

func NewUpstreamProcessorAggregator(
	eventProcessors []UpstreamStateEventProcessor,
) *UpstreamProcessorAggregator {
	processorsMap := make(map[EventProcessorType]UpstreamStateEventProcessor, len(eventProcessors))

	for _, eventProcessor := range eventProcessors {
		if eventProcessor == nil {
			continue
		}
		processorsMap[eventProcessor.Type()] = eventProcessor
	}

	return &UpstreamProcessorAggregator{
		eventProcessors: processorsMap,
	}
}
