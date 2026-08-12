package methods

import (
	"context"
	"fmt"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// MethodsProcessor aggregates a chain's MethodsDetectors into a stream of the
// upstream's unsupported-method set.
type MethodsProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[mapset.Set[string]]
}

type GenericMethodsProcessor struct {
	upstreamId string
	delay      time.Duration

	lifecycle  *utils.GenericLifecycle
	subManager *utils.SubscriptionManager[mapset.Set[string]]

	detectors []MethodsDetector
}

func NewGenericMethodsProcessor(
	ctx context.Context,
	upstreamId string,
	detectors []MethodsDetector,
	delay time.Duration,
) *GenericMethodsProcessor {
	if len(detectors) == 0 {
		return nil
	}

	name := fmt.Sprintf("%s_methods_processor", upstreamId)
	return &GenericMethodsProcessor{
		upstreamId: upstreamId,
		delay:      delay,
		lifecycle:  utils.NewGenericLifecycle(name, ctx),
		subManager: utils.NewSubscriptionManager[mapset.Set[string]](name),
		detectors:  detectors,
	}
}

func (m *GenericMethodsProcessor) Start() {
	m.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			published := m.detectAndPublish(ctx, nil)

			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(m.delay):
					published = m.detectAndPublish(ctx, published)
				}
			}
		}()
		return nil
	})
}

func (m *GenericMethodsProcessor) Stop() {
	log.Info().Msgf("stopping methods processor of upstream '%s'", m.upstreamId)
	m.lifecycle.Stop()
}

func (m *GenericMethodsProcessor) Running() bool {
	return m.lifecycle.Running()
}

func (m *GenericMethodsProcessor) Subscribe(name string) *utils.Subscription[mapset.Set[string]] {
	return m.subManager.Subscribe(name)
}

// detectAndPublish runs one round and publishes the merged verdict only when it differs
// from what was last published. Detection results are near-static, so on a periodic
// ticker the steady state is "nothing to say" - republishing an identical set would wake
// the upstream event loop and the chain-supervisor recompute for nothing. Returns the
// set now considered published.
func (m *GenericMethodsProcessor) detectAndPublish(ctx context.Context, published mapset.Set[string]) mapset.Set[string] {
	merged := m.detect(ctx)

	if published != nil && published.Equal(merged) {
		return published
	}

	log.Warn().Msgf("upstream '%s' does not support %d methods: %v", m.upstreamId, merged.Cardinality(), merged.ToSlice())
	m.subManager.Publish(merged.Clone())

	return merged
}

// detect runs every detector concurrently and unions their verdicts. Detectors are
// independent and additive - each names methods it knows to be absent - so a union needs
// no attribution or precedence between them.
func (m *GenericMethodsProcessor) detect(ctx context.Context) mapset.Set[string] {
	verdicts := make([]mapset.Set[string], len(m.detectors))

	var wg sync.WaitGroup
	for index, detector := range m.detectors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verdicts[index] = detector.DetectUnsupported(ctx)
		}()
	}
	wg.Wait()

	merged := mapset.NewThreadUnsafeSet[string]()
	for _, verdict := range verdicts {
		if verdict != nil {
			merged = merged.Union(verdict)
		}
	}

	return merged
}

var _ MethodsProcessor = (*GenericMethodsProcessor)(nil)
