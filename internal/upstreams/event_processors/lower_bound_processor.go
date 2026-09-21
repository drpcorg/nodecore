package event_processors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type LowerBoundEventProcessor interface {
	UpstreamStateEventProcessor
}

type GenericLowerBoundEventProcessor struct {
	lifecycle           *utils.GenericLifecycle
	upstreamId          string
	lowerBoundProcessor lower_bounds.LowerBoundProcessor
	emitter             Emitter
}

func (b *GenericLowerBoundEventProcessor) PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64 {
	return b.lowerBoundProcessor.PredictLowerBound(boundType, timeOffset)
}

func (b *GenericLowerBoundEventProcessor) Type() EventProcessorType {
	return LowerBoundEventProcessorType
}

func (b *GenericLowerBoundEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *GenericLowerBoundEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		b.lowerBoundProcessor.Start()

		boundSub := b.lowerBoundProcessor.Subscribe(fmt.Sprintf("%s_lower_bounds", b.upstreamId))

		go func() {
			defer boundSub.Unsubscribe()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping lower bounds events of upstream '%s'", b.upstreamId)
					return
				case bound, ok := <-boundSub.Events:
					if ok {
						b.emitter(&protocol.LowerBoundUpstreamStateEvent{Data: bound})
					}
				}
			}
		}()

		return nil
	})
}

func (b *GenericLowerBoundEventProcessor) Stop() {
	b.lifecycle.Stop()
	b.lowerBoundProcessor.Stop()
}

func (b *GenericLowerBoundEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func NewGenericLowerBoundEventProcessor(
	ctx context.Context,
	upstreamId string,
	lowerBoundProcessor lower_bounds.LowerBoundProcessor,
) *GenericLowerBoundEventProcessor {
	if lowerBoundProcessor == nil {
		return nil
	}

	return &GenericLowerBoundEventProcessor{
		lifecycle:           utils.NewGenericLifecycle(fmt.Sprintf("%s_lower_bounds_event_processor", upstreamId), ctx),
		upstreamId:          upstreamId,
		lowerBoundProcessor: lowerBoundProcessor,
	}
}

var _ LowerBoundEventProcessor = (*GenericLowerBoundEventProcessor)(nil)
