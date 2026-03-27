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

type BaseLowerBoundEventProcessor struct {
	lifecycle           *utils.BaseLifecycle
	upstreamId          string
	lowerBoundProcessor lower_bounds.LowerBoundProcessor
	emitter             Emitter
}

func (b *BaseLowerBoundEventProcessor) Type() EventProcessorType {
	return LowerBoundEventProcessorType
}

func (b *BaseLowerBoundEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *BaseLowerBoundEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go b.lowerBoundProcessor.Start()

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

func (b *BaseLowerBoundEventProcessor) Stop() {
	b.lifecycle.Stop()
	b.lowerBoundProcessor.Stop()
}

func (b *BaseLowerBoundEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func NewBaseLowerBoundEventProcessor(
	ctx context.Context,
	upstreamId string,
	lowerBoundProcessor lower_bounds.LowerBoundProcessor,
) *BaseLowerBoundEventProcessor {
	if lowerBoundProcessor == nil {
		return nil
	}

	return &BaseLowerBoundEventProcessor{
		lifecycle:           utils.NewBaseLifecycle(fmt.Sprintf("%s_lower_bounds_event_processor", upstreamId), ctx),
		upstreamId:          upstreamId,
		lowerBoundProcessor: lowerBoundProcessor,
	}
}

var _ LowerBoundEventProcessor = (*BaseLowerBoundEventProcessor)(nil)
