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
	CapEventProcessorType
	MethodsEventProcessorType
)

type UpstreamStateEventProcessor interface {
	utils.Lifecycle

	SetEmitter(emitter Emitter)
	Type() EventProcessorType
}

type Emitter func(event protocol.AbstractUpstreamStateEvent)

// UpstreamProcessorAggregator is driven from two goroutines - the supervisor (Resume,
// PartialStop) and the upstream state loop (head pause while syncing) - and needs no lock
// of its own: each processor makes its start and stop, including the nested processor it
// owns, one transition of its lifecycle (GenericLifecycle.Start / StopWith).
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

func (u *UpstreamProcessorAggregator) PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64 {
	processor, ok := u.eventProcessors[LowerBoundEventProcessorType]
	if !ok {
		return 0
	}
	lowerBoundProcessor, ok := processor.(interface {
		PredictLowerBound(protocol.LowerBoundType, int64) int64
	})
	if !ok {
		return 0
	}
	return lowerBoundProcessor.PredictLowerBound(boundType, timeOffset)
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

// IsProcessorRunning reports whether the processor of the given type is currently running;
// false when there is no such processor.
func (u *UpstreamProcessorAggregator) IsProcessorRunning(processorType EventProcessorType) bool {
	processor, ok := u.eventProcessors[processorType]
	return ok && processor.Running()
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
