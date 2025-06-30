package upstreams

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	choice "github.com/drpcorg/dsheltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

type UpstreamSupervisor interface {
	GetChainSupervisor(chain chains.Chain) *ChainSupervisor
	GetChainSupervisors() []*ChainSupervisor
	GetUpstream(string) *Upstream
	GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper]
	StartUpstreams()
}

type BaseUpstreamSupervisor struct {
	ctx              context.Context
	chainSupervisors *utils.CMap[chains.Chain, ChainSupervisor]
	upstreams        *utils.CMap[string, Upstream]
	eventsChan       chan protocol.UpstreamEvent
	upstreamsConfig  *config.UpstreamConfig
	executor         failsafe.Executor[*protocol.ResponseHolderWrapper]
	tracker          *dimensions.DimensionTracker
}

func NewBaseUpstreamSupervisor(ctx context.Context, upstreamsConfig *config.UpstreamConfig, tracker *dimensions.DimensionTracker) UpstreamSupervisor {
	return &BaseUpstreamSupervisor{
		ctx:              ctx,
		upstreams:        utils.NewCMap[string, Upstream](),
		chainSupervisors: utils.NewCMap[chains.Chain, ChainSupervisor](),
		eventsChan:       make(chan protocol.UpstreamEvent, 100),
		upstreamsConfig:  upstreamsConfig,
		tracker:          tracker,
		executor:         createFlowExecutor(upstreamsConfig.FailsafeConfig),
	}
}

func (u *BaseUpstreamSupervisor) GetChainSupervisors() []*ChainSupervisor {
	result := make([]*ChainSupervisor, 0)
	u.chainSupervisors.Range(func(key chains.Chain, val *ChainSupervisor) bool {
		result = append(result, val)
		return true
	})

	return result
}

func (u *BaseUpstreamSupervisor) GetChainSupervisor(chain chains.Chain) *ChainSupervisor {
	if c, ok := u.chainSupervisors.Load(chain); ok {
		return c
	}
	return nil
}

func (u *BaseUpstreamSupervisor) GetUpstream(upstreamId string) *Upstream {
	if up, ok := u.upstreams.Load(upstreamId); ok {
		return up
	}
	return nil
}

func (u *BaseUpstreamSupervisor) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return u.executor
}

func (u *BaseUpstreamSupervisor) StartUpstreams() {
	go u.processEvents()

	for _, upConfig := range u.upstreamsConfig.Upstreams {
		go func() {
			upstreamConnectorExecutor := createUpstreamExecutor(upConfig.FailsafeConfig)
			up, err := NewUpstream(u.ctx, upConfig, u.tracker, upstreamConnectorExecutor)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't create upstream %s", upConfig.Id)
				return
			}
			up.Start()

			u.upstreams.Store(up.Id, up)

			upSub := up.Subscribe(fmt.Sprintf("upstream_supervisor_%s_updates", up.Id))
			defer upSub.Unsubscribe()

			for {
				select {
				case <-u.ctx.Done():
					return
				case upstreamEvent, ok := <-upSub.Events:
					if ok {
						u.eventsChan <- upstreamEvent
					}
				}
			}
		}()
	}
}

func createFlowExecutor(failsafeConfig *config.FailsafeConfig) failsafe.Executor[*protocol.ResponseHolderWrapper] {
	policies := make([]failsafe.Policy[*protocol.ResponseHolderWrapper], 0)

	if failsafeConfig.HedgeConfig != nil {
		policies = append(policies, protocol.CreateFlowHedgePolicy(failsafeConfig.HedgeConfig))
	}

	return protocol.CreateFlowExecutor(policies...)
}

func createUpstreamExecutor(failsafeConfig *config.FailsafeConfig) failsafe.Executor[protocol.ResponseHolder] {
	policies := make([]failsafe.Policy[protocol.ResponseHolder], 0)

	if failsafeConfig.RetryConfig != nil {
		policies = append(policies, protocol.CreateUpstreamRetryPolicy(failsafeConfig.RetryConfig))
	}

	return protocol.CreateUpstreamExecutor(policies...)
}

func (u *BaseUpstreamSupervisor) processEvents() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case event, ok := <-u.eventsChan:
			if ok {
				chainSupervisor, exists := u.chainSupervisors.LoadOrStore(event.Chain, NewChainSupervisor(u.ctx, event.Chain, choice.NewHeightForkChoice(), u.tracker))

				if !exists {
					chainSupervisor.Start()
				}

				chainSupervisor.Publish(event)
			}
		}
	}
}
