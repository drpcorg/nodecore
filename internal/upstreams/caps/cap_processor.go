package caps

import (
	"context"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// CapProcessor aggregates a chain's CapDetectors into a single stream of the
// upstream's merged capability set, mirroring how LowerBoundProcessor aggregates
// LowerBoundDetectors.
type CapProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[mapset.Set[protocol.Cap]]
}

type capUpdate struct {
	index int
	caps  mapset.Set[protocol.Cap]
}

type GenericCapProcessor struct {
	upstreamId string

	lifecycle  *utils.GenericLifecycle
	subManager *utils.SubscriptionManager[mapset.Set[protocol.Cap]]

	detectors []CapDetector
}

func NewGenericCapProcessor(ctx context.Context, upstreamId string, detectors []CapDetector) *GenericCapProcessor {
	if len(detectors) == 0 {
		return nil
	}

	name := fmt.Sprintf("%s_cap_service", upstreamId)
	return &GenericCapProcessor{
		upstreamId: upstreamId,
		lifecycle:  utils.NewGenericLifecycle(name, ctx),
		subManager: utils.NewSubscriptionManager[mapset.Set[protocol.Cap]](name),
		detectors:  detectors,
	}
}

func (b *GenericCapProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		updates := make(chan capUpdate, 100)
		for i, detector := range b.detectors {
			go b.consume(ctx, i, detector, updates)
		}
		go b.aggregate(ctx, updates)
		return nil
	})
}

func (b *GenericCapProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *GenericCapProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *GenericCapProcessor) Subscribe(name string) *utils.Subscription[mapset.Set[protocol.Cap]] {
	return b.subManager.Subscribe(name)
}

// consume forwards one detector's snapshots onto the shared updates channel, tagged
// with the detector index so the aggregator can replace just that detector's slice
// of the merged set.
func (b *GenericCapProcessor) consume(ctx context.Context, index int, detector CapDetector, updates chan<- capUpdate) {
	capsChan := detector.DetectCaps(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case caps, ok := <-capsChan:
			if !ok {
				return
			}
			select {
			case updates <- capUpdate{index: index, caps: caps}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// aggregate keeps the latest snapshot per detector, unions them on every update, and
// publishes the merged set only when it changes.
func (b *GenericCapProcessor) aggregate(ctx context.Context, updates <-chan capUpdate) {
	latest := make([]mapset.Set[protocol.Cap], len(b.detectors))
	for i := range latest {
		latest[i] = mapset.NewThreadUnsafeSet[protocol.Cap]()
	}
	var published mapset.Set[protocol.Cap]

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.caps != nil {
				latest[update.index] = update.caps
			} else {
				latest[update.index] = mapset.NewThreadUnsafeSet[protocol.Cap]()
			}

			merged := mapset.NewThreadUnsafeSet[protocol.Cap]()
			for _, s := range latest {
				merged = merged.Union(s)
			}

			if published == nil || !published.Equal(merged) {
				published = merged
				log.Debug().Msgf("upstream '%s' caps changed to %v", b.upstreamId, merged.ToSlice())
				b.subManager.Publish(merged.Clone())
			}
		}
	}
}

var _ CapProcessor = (*GenericCapProcessor)(nil)
