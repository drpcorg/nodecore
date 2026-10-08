package upstreams

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

type GenericUpstreamSupervisor struct {
	ctx context.Context

	chainSupervisors *utils.CMap[chains.Chain, ChainSupervisor]
	upstreams        *utils.CMap[string, Upstream]

	eventsChan              chan protocol.UpstreamEvent
	upstreamsConfig         *config.UpstreamConfig
	executor                failsafe.Executor[*protocol.ResponseHolderWrapper]
	tracker                 dimensions.DimensionTracker
	statsService            UpstreamStatsService
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry

	torProxyUrl string

	// applyMu serializes StartUpstreams and ApplyUpstreams. It guards started,
	// managed, retired, upstreamIndices and upstreamIndicesCounter.
	applyMu sync.Mutex
	started bool
	// managed is the upstream set the supervisor is asked to run, by upstream id
	managed map[string]*managedUpstream
	// retired holds, per upstream id, the done channel of the instance that was
	// last told to stop, so that a new instance with the same id starts only
	// after the old one is completely gone
	retired map[string]<-chan struct{}
	// upstreamIndices binds an upstream id to its index for the process lifetime:
	// the index is part of sticky ids handed to clients, so an upstream that is
	// re-added or replaced must keep answering to the same one
	upstreamIndices        map[string]int
	upstreamIndicesCounter int

	subChainSupervisorManager *utils.SubscriptionManager[ChainSupervisorEvent]
}

// managedUpstream is one upstream of the managed set and the goroutine that owns it.
type managedUpstream struct {
	config *config.Upstream
	// cancel tells the owning goroutine to remove the upstream
	cancel context.CancelFunc
	// done is closed when the owning goroutine has exited, that is when the
	// upstream is stopped and its removal has been published
	done chan struct{}
}

const (
	maxUpstreamIndex = 1048575 // 0xfffff, which means that the next number will be 6 bytes
	// removalRoutingTimeout bounds how long a removal waits for the chain
	// supervisor to stop routing to the upstream before its connectors are stopped
	removalRoutingTimeout = 2 * time.Second
)

func NewGenericUpstreamSupervisor(
	ctx context.Context,
	upstreamsConfig *config.UpstreamConfig,
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
) UpstreamSupervisor {
	return &GenericUpstreamSupervisor{
		ctx:                       ctx,
		upstreams:                 utils.NewCMap[string, Upstream](),
		chainSupervisors:          utils.NewCMap[chains.Chain, ChainSupervisor](),
		eventsChan:                make(chan protocol.UpstreamEvent, 100),
		upstreamsConfig:           upstreamsConfig,
		tracker:                   tracker,
		statsService:              statsService,
		executor:                  createFlowExecutor(upstreamsConfig.FailsafeConfig),
		managed:                   make(map[string]*managedUpstream),
		retired:                   make(map[string]<-chan struct{}),
		upstreamIndices:           make(map[string]int),
		upstreamIndicesCounter:    1,
		rateLimitBudgetRegistry:   rateLimitBudgetRegistry,
		torProxyUrl:               torProxyUrl,
		subChainSupervisorManager: utils.NewSubscriptionManager[ChainSupervisorEvent]("chain_supervisor_events"),
	}
}

func (b *GenericUpstreamSupervisor) SubscribeChainSupervisor(name string) *utils.Subscription[ChainSupervisorEvent] {
	return b.subChainSupervisorManager.Subscribe(name)
}

func (b *GenericUpstreamSupervisor) GetChainSupervisors() []ChainSupervisor {
	result := make([]ChainSupervisor, 0)
	b.chainSupervisors.Range(func(key chains.Chain, val ChainSupervisor) bool {
		result = append(result, val)
		return true
	})

	return result
}

func (b *GenericUpstreamSupervisor) GetChainSupervisor(chain chains.Chain) ChainSupervisor {
	if c, ok := b.chainSupervisors.Load(chain); ok {
		return c
	}
	return nil
}

func (b *GenericUpstreamSupervisor) GetUpstream(upstreamId string) Upstream {
	if up, ok := b.upstreams.Load(upstreamId); ok {
		return up
	}
	return nil
}

func (b *GenericUpstreamSupervisor) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return b.executor
}

func (b *GenericUpstreamSupervisor) StartUpstreams() {
	log.Info().Msgf("upstreams will be started in %s mode", b.upstreamsConfig.Mode)

	go b.processEvents()

	b.applyMu.Lock()
	defer b.applyMu.Unlock()

	b.started = true
	for _, upConfig := range b.upstreamsConfig.Upstreams {
		b.startUpstream(upConfig, false)
	}
}

// startUpstream adds the upstream to the managed set and starts the goroutine
// that owns it. applyMu must be held.
func (b *GenericUpstreamSupervisor) startUpstream(upConfig *config.Upstream, reloaded bool) {
	upstreamIndex, ok := b.upstreamIndex(upConfig.Id)
	if !ok {
		log.Error().Msgf("upstream indices overflow, max is %d, upstream %s won't be started", maxUpstreamIndex, upConfig.Id)
		return
	}

	ctx, cancel := context.WithCancel(b.ctx)
	managed := &managedUpstream{config: upConfig, cancel: cancel, done: make(chan struct{})}
	previous := b.retired[upConfig.Id]
	delete(b.retired, upConfig.Id)
	b.managed[upConfig.Id] = managed

	go b.runUpstream(ctx, managed, previous, upstreamIndex, reloaded)
}

// upstreamIndex returns the index bound to the upstream id, allocating one on
// the first use. applyMu must be held.
func (b *GenericUpstreamSupervisor) upstreamIndex(upstreamId string) (int, bool) {
	if upstreamIndex, ok := b.upstreamIndices[upstreamId]; ok {
		return upstreamIndex, true
	}
	if b.upstreamIndicesCounter == maxUpstreamIndex {
		return 0, false
	}
	upstreamIndex := b.upstreamIndicesCounter
	b.upstreamIndicesCounter++
	b.upstreamIndices[upstreamId] = upstreamIndex
	return upstreamIndex, true
}

// runUpstream owns one upstream from creation to removal: it creates and starts
// it, forwards its events, and takes it down again once ctx is cancelled.
func (b *GenericUpstreamSupervisor) runUpstream(
	ctx context.Context,
	managed *managedUpstream,
	previous <-chan struct{},
	upstreamIndex int,
	reloaded bool,
) {
	defer close(managed.done)
	upConfig := managed.config

	if previous != nil {
		// the same id was running before: everything of the old instance,
		// its removal event included, must be out before this one shows up
		<-previous
	}
	if ctx.Err() != nil {
		return
	}

	up, upSub, err := b.createAndStartUpstream(upConfig, upstreamIndex, reloaded)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't create upstream %s", upConfig.Id)
		return
	}

	for {
		select {
		case <-ctx.Done():
			upSub.Unsubscribe()
			if b.ctx.Err() == nil {
				b.removeUpstream(up)
			}
			return
		case upstreamEvent, ok := <-upSub.Events:
			if ok {
				b.forwardUpstreamEvent(up, upstreamEvent)
			}
		}
	}
}

func (b *GenericUpstreamSupervisor) createAndStartUpstream(
	upConfig *config.Upstream,
	upstreamIndex int,
	reloaded bool,
) (up Upstream, upSub *utils.Subscription[protocol.UpstreamEvent], err error) {
	if reloaded {
		// At startup a panic here stops the process before it serves anything.
		// On a reload the process is already serving, so the upstream is given
		// up instead.
		defer func() {
			if r := recover(); r != nil {
				if upSub != nil {
					upSub.Unsubscribe()
					b.upstreams.Delete(upConfig.Id)
				}
				up, upSub, err = nil, nil, fmt.Errorf("panic during the upstream start: %v", r)
			}
		}()
	}

	upstreamConnectorExecutor := createUpstreamExecutor(upConfig.FailsafeConfig)
	up, err = CreateUpstream(b.ctx, upConfig, b.tracker, b.statsService, upstreamConnectorExecutor, upstreamIndex, b.rateLimitBudgetRegistry, b.torProxyUrl)
	if err != nil {
		return nil, nil, err
	}
	// Subscribe and register before Start() so no early event is lost: Start()
	// publishes InitUpstreamStateEvent - which carries the upstream's initial state,
	// including its config-defined labels - and Publish drops events that have no
	// subscriber. Storing the upstream first also keeps an event emitted during
	// Start() from reaching processEvents before GetUpstream can resolve it.
	upSub = up.Subscribe(fmt.Sprintf("upstream_supervisor_%s_updates", up.GetId()))
	b.upstreams.Store(up.GetId(), up)

	up.Start()

	return up, upSub, nil
}

// forwardUpstreamEvent passes an upstream's event on to the chain level. Pausing
// and resuming the upstream is done here, by the goroutine that owns it: an id
// can be handed over to a new instance, and a lookup by id further down the
// pipeline could hit the wrong one.
func (b *GenericUpstreamSupervisor) forwardUpstreamEvent(up Upstream, event protocol.UpstreamEvent) {
	switch event.EventType.(type) {
	case *protocol.RemoveUpstreamEvent:
		up.PartialStop()
	case *protocol.ValidUpstreamEvent:
		up.Resume()
	}
	b.publishEvent(event)
}

func (b *GenericUpstreamSupervisor) publishEvent(event protocol.UpstreamEvent) {
	select {
	case <-b.ctx.Done():
	case b.eventsChan <- event:
	}
}

// removeUpstream takes a running upstream out of the process. The order matters:
// the removal event goes through the same queue as the upstream's own events, so
// it is the last word on this instance; then routing is given time to drop the
// upstream; only then its connectors are closed.
func (b *GenericUpstreamSupervisor) removeUpstream(up Upstream) {
	b.publishEvent(protocol.UpstreamEvent{Id: up.GetId(), Chain: up.GetChain(), EventType: &protocol.RemoveUpstreamEvent{}})
	b.waitUntilNotRouted(up)

	b.upstreams.Delete(up.GetId())
	up.Stop()
	b.forgetUpstream(up)

	log.Info().Msgf("upstream %s of %s has been removed", up.GetId(), up.GetChain())
}

func (b *GenericUpstreamSupervisor) waitUntilNotRouted(up Upstream) {
	chainSupervisor, ok := b.chainSupervisors.Load(up.GetChain())
	if !ok {
		return
	}
	timeout := time.NewTimer(removalRoutingTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for chainSupervisor.GetUpstreamState(up.GetId()) != nil {
		select {
		case <-b.ctx.Done():
			return
		case <-timeout.C:
			log.Warn().Msgf("upstream %s is still routed %s after its removal, stopping it anyway", up.GetId(), removalRoutingTimeout)
			return
		case <-ticker.C:
		}
	}
}

// forgetUpstream drops what the rest of the process keeps per upstream, so that
// a removed upstream leaves no stale gauges behind and an upstream that comes
// back under the same id starts from a clean slate.
func (b *GenericUpstreamSupervisor) forgetUpstream(up Upstream) {
	if b.tracker != nil {
		b.tracker.RemoveUpstream(up.GetChain(), up.GetId())
	}
	labels := prometheus.Labels{"chain": up.GetChain().String(), "upstream": up.GetId()}
	availabilityMetric.DeletePartialMatch(labels)
	event_processors.DeleteUpstreamMetrics(up.GetChain(), up.GetId())
	ws.DeleteUpstreamMetrics(up.GetChain(), up.GetId())
}

func createFlowExecutor(failsafeConfig *config.FailsafeConfig) failsafe.Executor[*protocol.ResponseHolderWrapper] {
	policies := make([]failsafe.Policy[*protocol.ResponseHolderWrapper], 0)

	if failsafeConfig.HedgeConfig != nil {
		policies = append(policies, resilience.CreateFlowParallelHedgePolicy(failsafeConfig.HedgeConfig))
	}
	if failsafeConfig.RetryConfig != nil {
		policies = append(policies, resilience.CreateFlowRetryPolicy(failsafeConfig.RetryConfig))
	}

	return resilience.CreateFlowExecutor(policies...)
}

func createUpstreamExecutor(failsafeConfig *config.FailsafeConfig) failsafe.Executor[protocol.ResponseHolder] {
	policies := make([]failsafe.Policy[protocol.ResponseHolder], 0)

	if failsafeConfig.RetryConfig != nil {
		policies = append(policies, resilience.CreateUpstreamRetryPolicy(failsafeConfig.RetryConfig))
	}

	return resilience.CreateUpstreamExecutor(policies...)
}

func (b *GenericUpstreamSupervisor) processEvents() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case event, ok := <-b.eventsChan:
			if ok {
				if _, removed := event.EventType.(*protocol.RemoveUpstreamEvent); removed {
					// a removal has nothing to say to a chain that was never
					// created, and must not create it
					if chainSupervisor, exists := b.chainSupervisors.Load(event.Chain); exists {
						chainSupervisor.PublishUpstreamEvent(event)
					}
					continue
				}

				chainSupervisor, exists := b.chainSupervisors.LoadOrStoreLazy(event.Chain, func() ChainSupervisor {
					return NewGenericChainSupervisor(b.ctx, event.Chain, choice.NewHeightForkChoice, b.tracker, b.upstreamsConfig.ValidateLagFor(event.Chain.String()), b.GetUpstream)
				})

				if !exists {
					chainSupervisor.Start()
					b.subChainSupervisorManager.Publish(&AddChainSupervisorEvent{chainSupervisor})
				}

				chainSupervisor.PublishUpstreamEvent(event)
			}
		}
	}
}
