package event_processors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// MethodsEventProcessor turns the method-detection processor's verdicts into upstream
// state events.
type MethodsEventProcessor struct {
	lifecycle        *utils.GenericLifecycle
	upstreamId       string
	emitter          Emitter
	methodsProcessor methods.MethodsProcessor
}

func (m *MethodsEventProcessor) Start() {
	m.lifecycle.Start(func(ctx context.Context) error {
		methodsSub := m.methodsProcessor.Subscribe(fmt.Sprintf("%s_methods", m.upstreamId))
		m.methodsProcessor.Start()

		go func() {
			defer methodsSub.Unsubscribe()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping methods events of upstream '%s'", m.upstreamId)
					return
				case unsupported, ok := <-methodsSub.Events:
					if ok {
						m.emitter(&protocol.UnsupportedMethodsUpstreamStateEvent{Methods: unsupported})
					}
				}
			}
		}()

		return nil
	})
}

func (m *MethodsEventProcessor) Stop() {
	m.lifecycle.Stop()
	m.methodsProcessor.Stop()
}

func (m *MethodsEventProcessor) Running() bool {
	return m.lifecycle.Running()
}

func (m *MethodsEventProcessor) SetEmitter(emitter Emitter) {
	m.emitter = emitter
}

func (m *MethodsEventProcessor) Type() EventProcessorType {
	return MethodsEventProcessorType
}

func NewMethodsEventProcessor(
	ctx context.Context,
	upstreamId string,
	methodsProcessor methods.MethodsProcessor,
) *MethodsEventProcessor {
	if methodsProcessor == nil {
		return nil
	}

	return &MethodsEventProcessor{
		lifecycle:        utils.NewGenericLifecycle(fmt.Sprintf("%s_methods_event_processor", upstreamId), ctx),
		upstreamId:       upstreamId,
		methodsProcessor: methodsProcessor,
	}
}

var _ UpstreamStateEventProcessor = (*MethodsEventProcessor)(nil)
