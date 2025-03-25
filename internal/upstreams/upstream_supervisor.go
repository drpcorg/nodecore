package upstreams

import (
	"context"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	choice "github.com/drpcorg/dshaltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/drpcorg/dshaltie/pkg/utils"
)

type UpstreamSupervisor struct {
	ctx              context.Context
	chainSupervisors utils.CMap[chains.Chain, ChainSupervisor]
	upstreams        utils.CMap[string, Upstream]
	eventsChan       chan protocol.UpstreamEvent
}

func NewUpstreamSupervisor(ctx context.Context) *UpstreamSupervisor {
	return &UpstreamSupervisor{
		ctx:              ctx,
		upstreams:        utils.CMap[string, Upstream]{},
		chainSupervisors: utils.CMap[chains.Chain, ChainSupervisor]{},
		eventsChan:       make(chan protocol.UpstreamEvent, 100),
	}
}

func (u *UpstreamSupervisor) StartUpstreams(upstreamsConfigs *config.UpstreamConfig) {
	go u.processEvents()

	for _, upConfig := range upstreamsConfigs.Upstreams {
		go func() {
			up := NewUpstream(u.ctx, upConfig)
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

func (u *UpstreamSupervisor) processEvents() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case event, ok := <-u.eventsChan:
			if ok {
				chainSupervisor, exists := u.chainSupervisors.LoadOrStore(event.Chain, NewChainSupervisor(u.ctx, event.Chain, choice.NewHeightForkChoice()))

				if !exists {
					chainSupervisor.Start()
				}

				chainSupervisor.Publish(event)
			}
		}
	}
}
