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

type GenericCapEventProcessor struct {
	lifecycle    *utils.GenericLifecycle
	upstreamId   string
	capProcessor caps.CapProcessor
	emitter      Emitter
}

func (b *GenericCapEventProcessor) Type() EventProcessorType {
	return CapEventProcessorType
}

func (b *GenericCapEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *GenericCapEventProcessor) Start() {
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

func (b *GenericCapEventProcessor) Stop() {
	b.lifecycle.Stop()
	b.capProcessor.Stop()
}

func (b *GenericCapEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func NewGenericCapEventProcessor(ctx context.Context, upstreamId string, capProcessor caps.CapProcessor) *GenericCapEventProcessor {
	if capProcessor == nil {
		return nil
	}

	return &GenericCapEventProcessor{
		lifecycle:    utils.NewGenericLifecycle(fmt.Sprintf("%s_cap_event_processor", upstreamId), ctx),
		upstreamId:   upstreamId,
		capProcessor: capProcessor,
	}
}

var _ CapEventProcessor = (*GenericCapEventProcessor)(nil)
