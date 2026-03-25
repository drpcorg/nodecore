package event_processors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type LabelsEventProcessor struct {
	lifecycle       *utils.BaseLifecycle
	upstreamId      string
	emitter         Emitter
	labelsProcessor labels.LabelsProcessor
}

func (l *LabelsEventProcessor) Start() {
	l.lifecycle.Start(func(ctx context.Context) error {
		go l.labelsProcessor.Start()

		go func() {
			labelsSub := l.labelsProcessor.Subscribe(fmt.Sprintf("%s_labels", l.upstreamId))
			defer labelsSub.Unsubscribe()

			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping labels events of upstream '%s'", l.upstreamId)
					return
				case upstreamLabels, ok := <-labelsSub.Events:
					if ok {
						l.emitter(&protocol.LabelsUpstreamStateEvent{Labels: upstreamLabels})
					}
				}
			}
		}()

		return nil
	})
}

func (l *LabelsEventProcessor) Stop() {
	l.lifecycle.Stop()
	l.labelsProcessor.Stop()
}

func (l *LabelsEventProcessor) Running() bool {
	return l.lifecycle.Running()
}

func (l *LabelsEventProcessor) SetEmitter(emitter Emitter) {
	l.emitter = emitter
}

func (l *LabelsEventProcessor) Type() EventProcessorType {
	return LabelsProcessorType
}

func NewLabelsEventProcessor(
	ctx context.Context,
	upstreamId string,
	labelsProcessor labels.LabelsProcessor,
) *LabelsEventProcessor {
	if labelsProcessor == nil {
		return nil
	}

	return &LabelsEventProcessor{
		lifecycle:       utils.NewBaseLifecycle(fmt.Sprintf("%s_labels_event_processor", upstreamId), ctx),
		upstreamId:      upstreamId,
		labelsProcessor: labelsProcessor,
	}
}

var _ UpstreamStateEventProcessor = (*LabelsEventProcessor)(nil)
