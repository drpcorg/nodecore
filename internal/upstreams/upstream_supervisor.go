package upstreams

import (
	"context"
	"fmt"
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

type BaseUpstreamSupervisor struct {
	ctx context.Context

	chainSupervisors *utils.CMap[chains.Chain, ChainSupervisor]
	upstreams        *utils.CMap[string, Upstream]

	eventsChan              chan protocol.UpstreamEvent
	upstreamsConfig         *config.UpstreamConfig
	executor                failsafe.Executor[*protocol.ResponseHolderWrapper]
	tracker                 dimensions.DimensionTracker
	statsService            UpstreamStatsService
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry

	torProxyUrl            string
	upstreamIndicesCounter int

	subChainSupervisorManager *utils.SubscriptionManager[ChainSupervisorEvent]
}

func NewBaseUpstreamSupervisor(
	ctx context.Context,
	upstreamsConfig *config.UpstreamConfig,
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
) UpstreamSupervisor {
	return &BaseUpstreamSupervisor{
		ctx:                       ctx,
		upstreams:                 utils.NewCMap[string, Upstream](),
		chainSupervisors:          utils.NewCMap[chains.Chain, ChainSupervisor](),
		eventsChan:                make(chan protocol.UpstreamEvent, 100),
		upstreamsConfig:           upstreamsConfig,
		tracker:                   tracker,
		statsService:              statsService,
		executor:                  createFlowExecutor(upstreamsConfig.FailsafeConfig),
		upstreamIndicesCounter:    1,
		rateLimitBudgetRegistry:   rateLimitBudgetRegistry,
		torProxyUrl:               torProxyUrl,
		subChainSupervisorManager: utils.NewSubscriptionManager[ChainSupervisorEvent]("chain_supervisor_events"),
	}
}

func (b *BaseUpstreamSupervisor) SubscribeChainSupervisor(name string) *utils.Subscription[ChainSupervisorEvent] {
	return b.subChainSupervisorManager.Subscribe(name)
}

func (b *BaseUpstreamSupervisor) GetChainSupervisors() []ChainSupervisor {
	result := make([]ChainSupervisor, 0)
	b.chainSupervisors.Range(func(key chains.Chain, val ChainSupervisor) bool {
		result = append(result, val)
		return true
	})

	return result
}

func (b *BaseUpstreamSupervisor) GetChainSupervisor(chain chains.Chain) ChainSupervisor {
	if c, ok := b.chainSupervisors.Load(chain); ok {
		return c
	}
	return nil
}

func (b *BaseUpstreamSupervisor) GetUpstream(upstreamId string) Upstream {
	if up, ok := b.upstreams.Load(upstreamId); ok {
		return up
	}
	return nil
}

func (b *BaseUpstreamSupervisor) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return b.executor
}

func (b *BaseUpstreamSupervisor) StartUpstreams() {
	log.Info().Msgf("upstreams will be started in %s mode", b.upstreamsConfig.Mode)

	go b.processEvents()

	for _, upConfig := range b.upstreamsConfig.Upstreams {
		currentIndex := b.upstreamIndicesCounter
		if currentIndex == 1048575 { // 0xfffff, which means that the next number will be 6 bytes
			log.Error().Msgf("upstream indices overflow, max is %d", 1048575)
			break
		}

		b.upstreamIndicesCounter++

		go func(upstreamIndex int) {
			upstreamConnectorExecutor := createUpstreamExecutor(upConfig.FailsafeConfig)
			up, err := CreateUpstream(b.ctx, upConfig, b.tracker, b.statsService, upstreamConnectorExecutor, upstreamIndex, b.rateLimitBudgetRegistry, b.torProxyUrl)
			if err != nil {
				log.Error().Err(err).Msgf("couldn't create upstream %s", upConfig.Id)
				return
			}
			up.Start()

			b.upstreams.Store(up.GetId(), up)

			upSub := up.Subscribe(fmt.Sprintf("upstream_supervisor_%s_updates", up.GetId()))
			defer upSub.Unsubscribe()

			for {
				select {
				case <-b.ctx.Done():
					return
				case upstreamEvent, ok := <-upSub.Events:
					if ok {
						b.eventsChan <- upstreamEvent
					}
				}
			}
		}(currentIndex)
	}
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

func (b *BaseUpstreamSupervisor) processEvents() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case event, ok := <-b.eventsChan:
			if ok {
				chainSupervisor, exists := b.chainSupervisors.LoadOrStore(event.Chain, NewBaseChainSupervisor(b.ctx, event.Chain, choice.NewHeightForkChoice(), b.tracker))

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
			}
		}
	}
}
