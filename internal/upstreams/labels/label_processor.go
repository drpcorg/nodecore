package labels

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type LabelsProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[lo.Tuple2[string, string]]
}

type GenericLabelsProcessor struct {
	upstreamId string
	delay      time.Duration

	lifecycle  *utils.GenericLifecycle
	subManager *utils.SubscriptionManager[lo.Tuple2[string, string]]

	labelsDetectors []LabelsDetector
}

func (b *GenericLabelsProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		labelDetectorsChanArr := make([]<-chan lo.Tuple2[string, string], 0, len(b.labelsDetectors))
		for _, detector := range b.labelsDetectors {
			labelDetectorsChanArr = append(labelDetectorsChanArr, b.detectLabels(ctx, detector))
		}
		labelsChan := lo.FanIn(100, labelDetectorsChanArr...)

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case labels, ok := <-labelsChan:
					if ok {
						log.Info().Msgf("upstream '%s' label of %s is %s", b.upstreamId, labels.A, labels.B)
						b.subManager.Publish(labels)
					}
				}
			}
		}()
		return nil
	})
}

func (b *GenericLabelsProcessor) Stop() {
	log.Info().Msgf("stopping labels processor of upstream '%s'", b.upstreamId)
	b.lifecycle.Stop()
}

func (b *GenericLabelsProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *GenericLabelsProcessor) Subscribe(name string) *utils.Subscription[lo.Tuple2[string, string]] {
	return b.subManager.Subscribe(name)
}

func NewGenericLabelsProcessor(
	ctx context.Context,
	upstreamId string,
	labelsDetectors []LabelsDetector,
	delay time.Duration,
) *GenericLabelsProcessor {
	if len(labelsDetectors) == 0 {
		return nil
	}

	name := fmt.Sprintf("%s_labels_processor", upstreamId)
	return &GenericLabelsProcessor{
		upstreamId:      upstreamId,
		subManager:      utils.NewSubscriptionManager[lo.Tuple2[string, string]](name),
		lifecycle:       utils.NewGenericLifecycle(name, ctx),
		labelsDetectors: labelsDetectors,
		delay:           delay,
	}
}

func (b *GenericLabelsProcessor) detectLabels(ctx context.Context, detector LabelsDetector) chan lo.Tuple2[string, string] {
	labelsChan := make(chan lo.Tuple2[string, string], 10)

	go func() {
		defer close(labelsChan)
		detectLabelsAndSend(detector, labelsChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(b.delay):
				detectLabelsAndSend(detector, labelsChan)
			}
		}
	}()

	return labelsChan
}

func detectLabelsAndSend(detector LabelsDetector, labelsChan chan lo.Tuple2[string, string]) {
	labels := detector.DetectLabels()
	for key, value := range labels {
		if key == "" || value == "" {
			continue
		}
		labelsChan <- lo.T2(key, value)
	}
}

var _ LabelsProcessor = (*GenericLabelsProcessor)(nil)
