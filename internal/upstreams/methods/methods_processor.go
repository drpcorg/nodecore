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

// DetectionInterval is how often an upstream's methods are re-detected. It is a flat
// constant rather than a multiple of validation-interval so that tightening health checks
// cannot silently multiply detection traffic: the answer changes only when a node is
// restarted with different --http.api flags, which is measured in months. dshackle detects
// once at launch and never again, so re-detecting hourly is already the more responsive
// end of the trade.
const DetectionInterval = time.Hour

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
			// latest holds each detector's most recent non-nil verdict for the lifetime of
			// this run, so a detector that cannot answer keeps contributing what it last
			// established instead of silently dropping out of the merge. It lives here
			// rather than on the struct because this goroutine is its only owner, and a
			// restarted processor should start over with no history.
			latest := make([]mapset.Set[string], len(m.detectors))
			published := m.detectAndPublish(ctx, latest, nil)

			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(m.delay):
					published = m.detectAndPublish(ctx, latest, published)
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
func (m *GenericMethodsProcessor) detectAndPublish(
	ctx context.Context,
	latest []mapset.Set[string],
	published mapset.Set[string],
) mapset.Set[string] {
	merged, anyOpinion := m.detect(ctx, latest)

	// No detector has ever managed to answer. The empty merge here means "we know
	// nothing", not "everything is supported", so publishing it would strip nothing and
	// look like a real verdict. Hold; the upstream keeps its full spec, which is the
	// behaviour it had before detection existed.
	if !anyOpinion {
		log.Debug().Msgf("no method detector of upstream '%s' has an answer yet", m.upstreamId)
		return published
	}

	if published != nil && published.Equal(merged) {
		return published
	}

	if !merged.IsEmpty() {
		log.Warn().Msgf("upstream '%s' does not support %d methods: %v", m.upstreamId, merged.Cardinality(), merged.ToSlice())
	}
	m.subManager.Publish(merged.Clone())

	return merged
}

// detect runs every detector concurrently and unions their verdicts. Detectors are
// independent and additive - each names methods it knows to be absent - so a union needs
// no attribution or precedence between them.
//
// A detector's slot in latest is replaced only when that detector answers: nil is "I have
// never found out" (see MethodsDetector), and dropping such a detector's contribution from
// the merge would restore every method it had previously stripped. An empty non-nil set is
// an answer and does clear the slot, so a node that gains modules still converges.
//
// The second return value reports whether any detector has ever answered.
func (m *GenericMethodsProcessor) detect(ctx context.Context, latest []mapset.Set[string]) (mapset.Set[string], bool) {
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
	anyOpinion := false
	for index, verdict := range verdicts {
		if verdict != nil {
			latest[index] = verdict
		}
		if latest[index] == nil {
			continue
		}
		anyOpinion = true
		merged = merged.Union(latest[index])
	}

	return merged, anyOpinion
}

var _ MethodsProcessor = (*GenericMethodsProcessor)(nil)
