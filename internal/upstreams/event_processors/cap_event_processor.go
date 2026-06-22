package event_processors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type CapEventProcessor interface {
	UpstreamStateEventProcessor
}

type BaseCapEventProcessor struct {
	lifecycle    *utils.BaseLifecycle
	upstreamId   string
	capProcessor caps.CapProcessor
	emitter      Emitter
}

func (b *BaseCapEventProcessor) Type() EventProcessorType {
	return CapEventProcessorType
}

func (b *BaseCapEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *BaseCapEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		// Subscribe before starting the processor so the initial merged cap set,
		// published as soon as the first detector reports, is never missed.
		capSub := b.capProcessor.Subscribe(fmt.Sprintf("%s_caps", b.upstreamId))
		b.capProcessor.Start()

		go func() {
			defer capSub.Unsubscribe()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping cap events of upstream '%s'", b.upstreamId)
					return
				case capsSet, ok := <-capSub.Events:
					if ok {
						b.emitter(&protocol.CapsUpstreamStateEvent{Caps: capsSet})
					}
				}
			}
		}()

		return nil
	})
}

func (b *BaseCapEventProcessor) Stop() {
	b.lifecycle.Stop()
	b.capProcessor.Stop()
}

func (b *BaseCapEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func NewBaseCapEventProcessor(ctx context.Context, upstreamId string, capProcessor caps.CapProcessor) *BaseCapEventProcessor {
	if capProcessor == nil {
		return nil
	}

	return &BaseCapEventProcessor{
		lifecycle:    utils.NewBaseLifecycle(fmt.Sprintf("%s_cap_event_processor", upstreamId), ctx),
		upstreamId:   upstreamId,
		capProcessor: capProcessor,
	}
}

var _ CapEventProcessor = (*BaseCapEventProcessor)(nil)
