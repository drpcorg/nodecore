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
	// manualBounds are configured bounds; set once before Start and read-only afterwards
	manualBounds map[protocol.LowerBoundType]int64
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
	return newGenericLowerBoundProcessor(ctx, upstreamId, averageSpeed, initialDelay, lowerBoundsDetectors)
}

func newGenericLowerBoundProcessor(
	ctx context.Context,
	upstreamId string,
	averageSpeed float64,
	initialDelay time.Duration,
	lowerBoundsDetectors []LowerBoundDetector,
) *GenericLowerBoundProcessor {
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

// WithManualBounds pins the configured bound types: they are published once at Start,
// their detectors are dropped, and detector output for them is discarded.
func WithManualBounds(
	ctx context.Context,
	upstreamId string,
	manual map[protocol.LowerBoundType]int64,
	detected LowerBoundProcessor,
) LowerBoundProcessor {
	if len(manual) == 0 {
		return detected
	}

	var processor *GenericLowerBoundProcessor
	switch p := detected.(type) {
	case nil:
	case *GenericLowerBoundProcessor:
		processor = p
	default:
		log.Panic().Msgf("upstream '%s': lower-bounds needs a *GenericLowerBoundProcessor, got %T", upstreamId, detected)
	}
	if processor == nil {
		processor = newGenericLowerBoundProcessor(ctx, upstreamId, 0, 15*time.Second, nil)
	}

	processor.manualBounds = manual
	processor.lowerBoundsDetectors = lo.Filter(processor.lowerBoundsDetectors, func(detector LowerBoundDetector, _ int) bool {
		keep := lo.ContainsBy(detector.SupportedTypes(), func(t protocol.LowerBoundType) bool {
			_, configured := manual[t]
			return t != protocol.UnknownBound && !configured
		})
		if !keep {
			log.Info().Msgf("upstream '%s': lower bound detection of %s skipped, bounds are configured", upstreamId, detector.SupportedTypes())
		}
		return keep
	})
	return processor
}

func (b *GenericLowerBoundProcessor) PredictLowerBound(bt protocol.LowerBoundType, timeOffset int64) int64 {
	if bound, ok := b.manualBounds[bt]; ok {
		return bound
	}
	return b.lowerBounds.PredictNextBound(bt, timeOffset)
}

func (b *GenericLowerBoundProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		for boundType, bound := range b.manualBounds {
			log.Info().Msgf("upstream '%s' lower bound of type %s is %d (configured)", b.upstreamId, boundType.String(), bound)
			b.subManager.Publish(protocol.NewLowerBoundDataNow(bound, boundType))
		}
		// lo.FanIn over zero channels yields a closed channel, the select below would spin on it
		if len(b.lowerBoundsDetectors) == 0 {
			return nil
		}

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
		if _, configured := b.manualBounds[data.Type]; configured {
			continue
		}
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
