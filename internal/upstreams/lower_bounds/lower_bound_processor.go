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

type BaseLowerBoundProcessor struct {
	upstreamId   string
	initialDelay time.Duration

	lifecycle  *utils.BaseLifecycle
	subManager *utils.SubscriptionManager[protocol.LowerBoundData]

	lowerBoundsDetectors []LowerBoundDetector
	lowerBounds          *LowerBounds
}

func (b *BaseLowerBoundProcessor) PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64 {
	return b.lowerBounds.PredictNextBound(boundType, timeOffset)
}

func (b *BaseLowerBoundProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		lowerBoundsChansArr := make([]<-chan protocol.LowerBoundData, 0, len(b.lowerBoundsDetectors))
		for _, detector := range b.lowerBoundsDetectors {
			lowerBoundsChansArr = append(lowerBoundsChansArr, b.detectLowerBound(ctx, detector))
		}
		lowerBoundChan := lo.FanIn(100, lowerBoundsChansArr...)

		for {
			select {
			case <-ctx.Done():
				return nil
			case lowerBound, ok := <-lowerBoundChan:
				if ok {
					log.Info().Msgf("upstream '%s' lower bound of type %s is %d", b.upstreamId, lowerBound.Type.String(), lowerBound.Bound)
					b.subManager.Publish(lowerBound)
				}
			}
		}
	})
}

func (b *BaseLowerBoundProcessor) Stop() {
	log.Info().Msgf("stopping lower bounds service of upstream '%s'", b.upstreamId)
	b.lifecycle.Stop()
}

func (b *BaseLowerBoundProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *BaseLowerBoundProcessor) Subscribe(name string) *utils.Subscription[protocol.LowerBoundData] {
	return b.subManager.Subscribe(name)
}

func NewBaseLowerBoundProcessor(
	ctx context.Context,
	upstreamId string,
	averageSpeed float64,
	lowerBoundsDetectors []LowerBoundDetector,
) *BaseLowerBoundProcessor {
	return NewBaseLowerBoundProcessorWithDelay(ctx, upstreamId, averageSpeed, 15*time.Second, lowerBoundsDetectors)
}

func NewBaseLowerBoundProcessorWithDelay(
	ctx context.Context,
	upstreamId string,
	averageSpeed float64,
	initialDelay time.Duration,
	lowerBoundsDetectors []LowerBoundDetector,
) *BaseLowerBoundProcessor {
	name := fmt.Sprintf("%s_lower_bound_service", upstreamId)
	return &BaseLowerBoundProcessor{
		upstreamId:           upstreamId,
		initialDelay:         initialDelay,
		subManager:           utils.NewSubscriptionManager[protocol.LowerBoundData](name),
		lifecycle:            utils.NewBaseLifecycle(name, ctx),
		lowerBoundsDetectors: lowerBoundsDetectors,
		lowerBounds:          NewLowerBounds(averageSpeed),
	}
}

func (b *BaseLowerBoundProcessor) detectLowerBound(ctx context.Context, detector LowerBoundDetector) chan protocol.LowerBoundData {
	boundsChan := make(chan protocol.LowerBoundData, 10)

	go func() {
		// delay detection the first bound
		time.Sleep(b.initialDelay)
		b.processBounds(detector, boundsChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(detector.Period()):
				b.processBounds(detector, boundsChan)
			}
		}
	}()

	return boundsChan
}

func (b *BaseLowerBoundProcessor) processBounds(detector LowerBoundDetector, boundsChan chan protocol.LowerBoundData) {
	bounds, err := detector.DetectLowerBound()
	if err != nil {
		log.
			Error().
			Err(err).
			Msgf("couldn't detect lower bounds %s for upstream '%s'", detector.SupportedTypes(), b.upstreamId)
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
			b.lowerBounds.UpdateBound(data)
			boundsChan <- data
		}
	}
}

var _ LowerBoundProcessor = (*BaseLowerBoundProcessor)(nil)
