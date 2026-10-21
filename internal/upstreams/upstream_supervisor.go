package upstreams

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/resilience"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

const (
	// maxUpstreamIndex is 0xfffff: the next value would need six hex digits in the sticky hash.
	maxUpstreamIndex = 1048575
	// removalGracePeriod separates deregistering an upstream from stopping it, so the unary
	// requests already on it can finish; by then it is no longer selected.
	removalGracePeriod = 5 * time.Second
	// removalDoneTimeout bounds the wait for the forwarder of a removed upstream to exit. The
	// forwarder only reaches its loop once the instance's Start returned, so the bound must
	// outlast a slow settings validation; past it the removal goes on so a wedged event
	// pipeline can never hang a reload.
	removalDoneTimeout = 60 * time.Second
)

type upstreamFactory func(ctx context.Context, conf *config.Upstream, executor failsafe.Executor[protocol.ResponseHolder], index int) (Upstream, error)

// supervisorEvent is an upstream event on its way to processEvents. handled, when set, is
// closed once processEvents has dealt with the event; the forwarder of a removed upstream
// waits on it so that its removal is complete only after the Remove event was handled.
type supervisorEvent struct {
	event   protocol.UpstreamEvent
	handled chan struct{}
}

// runningUpstream is one upstream the supervisor runs. conf is the effective config it was
// built from and the thing ApplyUpstreams compares. stop tells the forwarder to deregister
// the upstream; done is closed when the forwarder has done so and exited.
type runningUpstream struct {
	conf     *config.Upstream
	upstream Upstream
	stop     chan struct{}
	done     chan struct{}
}

type GenericUpstreamSupervisor struct {
	ctx context.Context

	chainSupervisors *utils.CMap[chains.Chain, ChainSupervisor]
	// upstreams holds every running upstream by id. GetUpstream reads it lock-free on the
	// request path; it is only ever written by ApplyUpstreams, under applyMu.
	upstreams *utils.CMap[string, *runningUpstream]
	applyMu   sync.Mutex

	eventsChan      chan supervisorEvent
	upstreamsConfig *config.UpstreamConfig
	executor        failsafe.Executor[*protocol.ResponseHolderWrapper]
	tracker         dimensions.DimensionTracker

	upstreamIndicesCounter int
	// createUpstream builds an instance from its effective config; it carries everything
	// creation needs (stats, budgets, tor) so the supervisor does not have to.
	createUpstream upstreamFactory
	removalGrace   time.Duration

	subChainSupervisorManager *utils.SubscriptionManager[ChainSupervisorEvent]
}

func NewGenericUpstreamSupervisor(
	ctx context.Context,
	upstreamsConfig *config.UpstreamConfig,
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
) UpstreamSupervisor {
	supervisor := &GenericUpstreamSupervisor{
		ctx:                       ctx,
		upstreams:                 utils.NewCMap[string, *runningUpstream](),
		chainSupervisors:          utils.NewCMap[chains.Chain, ChainSupervisor](),
		eventsChan:                make(chan supervisorEvent, 100),
		upstreamsConfig:           upstreamsConfig,
		tracker:                   tracker,
		executor:                  createFlowExecutor(upstreamsConfig.FailsafeConfig),
		upstreamIndicesCounter:    1,
		removalGrace:              removalGracePeriod,
		subChainSupervisorManager: utils.NewSubscriptionManager[ChainSupervisorEvent]("chain_supervisor_events"),
	}
	supervisor.createUpstream = func(ctx context.Context, conf *config.Upstream, executor failsafe.Executor[protocol.ResponseHolder], index int) (Upstream, error) {
		return CreateUpstream(ctx, conf, tracker, statsService, executor, index, rateLimitBudgetRegistry, torProxyUrl)
	}
	return supervisor
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
	if running, ok := b.upstreams.Load(upstreamId); ok {
		return running.upstream
	}
	return nil
}

func (b *GenericUpstreamSupervisor) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return b.executor
}

func (b *GenericUpstreamSupervisor) StartUpstreams() {
	log.Info().Msgf("upstreams will be started in %s mode", b.upstreamsConfig.Mode)

	go b.processEvents()

	b.ApplyUpstreams(b.upstreamsConfig.Upstreams)
}

// ApplyUpstreams holds applyMu for its whole duration, so the initial start and any reload
// run one after another. Removals run in parallel and are waited for; additions then run in
// parallel and are not: an addition is complete when the instance is created and stored,
// its Start and health are asynchronous as they always were.
func (b *GenericUpstreamSupervisor) ApplyUpstreams(confs []*config.Upstream) UpstreamsDiff {
	b.applyMu.Lock()
	defer b.applyMu.Unlock()

	var diff UpstreamsDiff
	toRemove := make([]*runningUpstream, 0)
	toAdd := make([]*config.Upstream, 0)

	wanted := make(map[string]*config.Upstream, len(confs))
	for _, conf := range confs {
		wanted[conf.Id] = conf
	}
	b.upstreams.Range(func(id string, running *runningUpstream) bool {
		want, ok := wanted[id]
		switch {
		case !ok:
			diff.Removed = append(diff.Removed, id)
			toRemove = append(toRemove, running)
		case !reflect.DeepEqual(running.conf, want):
			diff.Changed = append(diff.Changed, id)
			toRemove = append(toRemove, running)
			toAdd = append(toAdd, want)
		}
		return true
	})
	for _, conf := range confs {
		if _, ok := b.upstreams.Load(conf.Id); !ok {
			diff.Added = append(diff.Added, conf.Id)
			toAdd = append(toAdd, conf)
		}
	}
	slices.Sort(diff.Added)
	slices.Sort(diff.Removed)
	slices.Sort(diff.Changed)

	var removals sync.WaitGroup
	for _, running := range toRemove {
		removals.Go(func() { b.removeUpstream(running) })
	}
	removals.Wait()

	b.addUpstreams(toAdd)
	return diff
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

// addUpstreams creates the instances in parallel and waits for that, then stores each one
// and starts its forwarder. Creation is local work (no dial, no probe) and must have finished
// before the entry exists. A creation failure is a config error (connector settings, chain
// specifics) and is only logged: nothing is stored for the id, so it is built again when a
// changed config is applied, never retried on its own. Called with applyMu held.
func (b *GenericUpstreamSupervisor) addUpstreams(confs []*config.Upstream) {
	created := make([]Upstream, len(confs))
	var creations sync.WaitGroup
	for i, conf := range confs {
		index := b.upstreamIndicesCounter
		if index == maxUpstreamIndex {
			log.Error().Msgf("upstream indices overflow, max is %d, upstream %s is not started", maxUpstreamIndex, conf.Id)
			continue
		}
		b.upstreamIndicesCounter++

		creations.Go(func() {
			up, err := b.createUpstream(b.ctx, conf, createUpstreamExecutor(conf.FailsafeConfig), index)
			if err != nil {
				log.Error().Err(err).Msgf("couldn't create upstream %s", conf.Id)
				return
			}
			created[i] = up
		})
	}
	creations.Wait()

	for i, up := range created {
		if up == nil {
			continue
		}
		running := &runningUpstream{
			conf:     confs[i],
			upstream: up,
			stop:     make(chan struct{}),
			done:     make(chan struct{}),
		}
		// Stored before Start so an event emitted during Start never reaches processEvents
		// before GetUpstream can resolve it.
		b.upstreams.Store(up.GetId(), running)
		go b.runUpstream(running)
	}
}

// runUpstream subscribes, starts the upstream and forwards its events into eventsChan.
// Subscribing before Start keeps the InitUpstreamStateEvent that Start publishes, which
// carries the initial state including the config-defined labels. On stop it pushes a
// synthesized RemoveUpstreamEvent as its last event, waits until processEvents has handled
// it and exits: that exit is the deregistration, nothing the instance publishes afterwards
// has a subscriber. The wait matters for a replaced id: processEvents resolves the Remove
// against the upstream map when it dequeues it, so the replacement must not be stored
// under the same id before then.
func (b *GenericUpstreamSupervisor) runUpstream(running *runningUpstream) {
	defer close(running.done)
	up := running.upstream

	upSub := up.Subscribe(fmt.Sprintf("upstream_supervisor_%s_updates", up.GetId()))
	defer upSub.Unsubscribe()

	up.Start()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-running.stop:
			removeEvent := supervisorEvent{
				event:   protocol.UpstreamEvent{Id: up.GetId(), Chain: up.GetChain(), EventType: &protocol.RemoveUpstreamEvent{}},
				handled: make(chan struct{}),
			}
			select {
			case b.eventsChan <- removeEvent:
			case <-b.ctx.Done():
				return
			}
			select {
			case <-removeEvent.handled:
			case <-b.ctx.Done():
			}
			return
		case upstreamEvent, ok := <-upSub.Events:
			if ok {
				b.eventsChan <- supervisorEvent{event: upstreamEvent}
			}
		}
	}
}

// removeUpstream deregisters, waits the grace, stops and forgets one upstream. Stop may block
// for as long as the instance's settings validation if it is still validating; removals run
// in parallel, so that does not add up.
func (b *GenericUpstreamSupervisor) removeUpstream(running *runningUpstream) {
	id := running.conf.Id
	close(running.stop)
	select {
	case <-running.done:
	case <-time.After(removalDoneTimeout):
		log.Warn().Msgf("the event forwarder of upstream %s did not exit within %s, stopping the upstream anyway", id, removalDoneTimeout)
	}

	select {
	case <-time.After(b.removalGrace):
	case <-b.ctx.Done():
	}

	running.upstream.Stop()
	// deleted only now: during the grace the upstream is still resolvable for the requests
	// already on it
	b.upstreams.Delete(id)
	log.Info().Msgf("upstream %s has been removed", id)
}

func (b *GenericUpstreamSupervisor) processEvents() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case supervisorEvt, ok := <-b.eventsChan:
			if ok {
				event := supervisorEvt.event
				chainSupervisor, exists := b.chainSupervisors.LoadOrStoreLazy(event.Chain, func() ChainSupervisor {
					return NewGenericChainSupervisor(b.ctx, event.Chain, choice.NewHeightForkChoice, b.tracker, b.upstreamsConfig.ValidateLagFor(event.Chain.String()), b.GetUpstream)
				})

				if !exists {
					chainSupervisor.Start()
					b.subChainSupervisorManager.Publish(&AddChainSupervisorEvent{chainSupervisor})
				}

				switch event.EventType.(type) {
				case *protocol.RemoveUpstreamEvent:
					upstream := b.GetUpstream(event.Id)
					if upstream != nil {
						upstream.PartialStop()
					}
				case *protocol.ValidUpstreamEvent:
					upstream := b.GetUpstream(event.Id)
					if upstream != nil {
						upstream.Resume()
					}
				}

				chainSupervisor.PublishUpstreamEvent(event)
				if supervisorEvt.handled != nil {
					close(supervisorEvt.handled)
				}
			}
		}
	}
}
