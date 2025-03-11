package upstreams

import (
	"github.com/drpcorg/dshaltie/src/config"
	"github.com/drpcorg/dshaltie/src/utils"
)

type UpstreamSupervisor struct {
	upstreams utils.CMap[string, Upstream]
}

func NewUpstreamSupervisor() *UpstreamSupervisor {
	return &UpstreamSupervisor{
		upstreams: utils.CMap[string, Upstream]{},
	}
}

func (u *UpstreamSupervisor) StartUpstreams(upstreamsConfigs []*config.UpstreamConfig) {
	for _, upConfig := range upstreamsConfigs {
		go func() {
			up := NewUpstream(upConfig)
			up.Start()

			u.upstreams.Store(up.Id, up)
		}()
	}
}
