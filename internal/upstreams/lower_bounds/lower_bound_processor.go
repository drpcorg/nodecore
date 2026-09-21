package lower_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type LowerBoundProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[protocol.LowerBoundData]
	PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64
}

type GenericLowerBoundProcessor struct {
	upstreamId   string
	initialDelay time.Duration

	lifecycle  *utils.GenericLifecycle
	subManager *utils.SubscriptionManager[protocol.LowerBoundData]

	lowerBoundsDetectors []LowerBoundDetector
	lowerBounds          *LowerBounds
}

func NewGenericLowerBoundProcessor(
	ctx context.Context,
	upstreamId string,
	averageSpeed float64,
	lowerBoundsDetectors []LowerBoundDetector,
) *GenericLowerBoundProcessor {
	return NewGenericLowerBoundProcessorWithDelay(
		ctx, upstreamId, averageSpeed, 15*time.Second, lowerBoundsDetectors,
	)
}

func NewGenericLowerBoundProcessorWithDelay(
	ctx context.Context,
	upstreamId string,
	averageSpeed float64,
	initialDelay time.Duration,
	lowerBoundsDetectors []LowerBoundDetector,
) *GenericLowerBoundProcessor {
	if len(lowerBoundsDetectors) == 0 {
		return nil
	}

	name := fmt.Sprintf("%s_lower_bound_service", upstreamId)
	return &GenericLowerBoundProcessor{
		upstreamId:           upstreamId,
		initialDelay:         initialDelay,
		subManager:           utils.NewSubscriptionManager[protocol.LowerBoundData](name),
		lifecycle:            utils.NewGenericLifecycle(name, ctx),
		lowerBoundsDetectors: lowerBoundsDetectors,
		lowerBounds:          NewLowerBounds(averageSpeed),
	}
}

func (b *GenericLowerBoundProcessor) PredictLowerBound(bt protocol.LowerBoundType, timeOffset int64) int64 {
	return b.lowerBounds.PredictNextBound(bt, timeOffset)
}

func (b *GenericLowerBoundProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		lowerBoundsChansArr := make([]<-chan protocol.LowerBoundData, 0, len(b.lowerBoundsDetectors))
		for _, detector := range b.lowerBoundsDetectors {
			lowerBoundsChansArr = append(lowerBoundsChansArr, b.detectLowerBound(ctx, detector))
		}
		lowerBoundChan := lo.FanIn(100, lowerBoundsChansArr...)

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case lowerBound, ok := <-lowerBoundChan:
					if ok {
						log.Info().Msgf("upstream '%s' lower bound of type %s is %d", b.upstreamId, lowerBound.Type.String(), lowerBound.Bound)
						b.subManager.Publish(lowerBound)
					}
				}
			}
		}()
		return nil
	})
}

func (b *GenericLowerBoundProcessor) Stop() {
	log.Info().Msgf("stopping lower bounds service of upstream '%s'", b.upstreamId)
	b.lifecycle.Stop()
}

func (b *GenericLowerBoundProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *GenericLowerBoundProcessor) Subscribe(name string) *utils.Subscription[protocol.LowerBoundData] {
	return b.subManager.Subscribe(name)
}

func (b *GenericLowerBoundProcessor) detectLowerBound(
	ctx context.Context,
	detector LowerBoundDetector,
) chan protocol.LowerBoundData {
	boundsChan := make(chan protocol.LowerBoundData, 10)

	go func() {
		defer close(boundsChan)
		// delay detection the first bound
		time.Sleep(b.initialDelay)
		b.processBounds(ctx, detector, boundsChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(detector.Period()):
				b.processBounds(ctx, detector, boundsChan)
			}
		}
	}()

	return boundsChan
}

func (b *GenericLowerBoundProcessor) processBounds(
	ctx context.Context,
	detector LowerBoundDetector,
	boundsChan chan protocol.LowerBoundData,
) {
	bounds, err := detector.DetectLowerBound(ctx)
	if err != nil {
		log.
			Error().
			Err(err).
			Msgf(
				"couldn't detect lower bounds %s for upstream '%s'",
				detector.SupportedTypes(), b.upstreamId,
			)
		return
	}

	for _, data := range bounds {
		var bound int64
		lastBound, ok := b.lowerBounds.GetLastBound(data.Type)
		if !ok {
			bound = 0
		} else {
			bound = lastBound.Bound
		}

		if data.Bound >= bound || data.Bound == 1 {
			b.publishBound(data, boundsChan)
		}
	}
}

func (b *GenericLowerBoundProcessor) publishBound(data protocol.LowerBoundData, boundsChan chan protocol.LowerBoundData) {
	b.lowerBounds.UpdateBound(data)
	boundsChan <- data
}

var _ LowerBoundProcessor = (*GenericLowerBoundProcessor)(nil)
