package emerald

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
)

// SubscribeUpstreamStatusWithResync forwards domain events through one Send loop.
// Each chain subscription controls its own request-specific coalescing interval.
func SubscribeUpstreamStatusWithResync(supervisor upstreams.UpstreamSupervisor, stream dshackle.Blockchain_SubscribeUpstreamStatusServer, interval, resync time.Duration) error {
	if supervisor == nil {
		return errNilUpstreamSupervisor
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	events := make(chan *upstreams.UpstreamStateEvent, 100)
	registry := supervisor.SubscribeChainSupervisor("upstream_status")
	defer registry.Unsubscribe()
	subscribed := make(map[chains.Chain]bool)
	discover := func() {
		for _, chain := range supervisor.GetChainSupervisors() {
			if chain == nil || subscribed[chain.GetChain()] {
				continue
			}
			subscribed[chain.GetChain()] = true
			chain.SubscribeUpstreamStates(ctx, "upstream_status", interval, resync, events)
		}
	}
	discover()
	ticker := time.NewTicker(resync)
	defer ticker.Stop()
	registryEvents := registry.Events
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-registryEvents:
			if !open {
				registryEvents = nil
				continue
			}
			discover()
		case <-ticker.C:
			// Registry notifications, like state fan-out, can be dropped under pressure.
			discover()
		case event := <-events:
			ref := chains.GetChain(event.Chain.String()).GrpcId
			var response *dshackle.SubscribeChainStatusResponse
			if event.Full {
				response = toFullResponse(ref, event.State)
			} else {
				response = stateWrappersToResponse(ref, event.Wrappers)
			}
			response.UpstreamId, response.UpstreamIndex = event.ID, event.Index
			if err := stream.Send(response); err != nil {
				return err
			}
		}
	}
}
