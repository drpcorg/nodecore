package emerald

import (
	"context"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/rs/zerolog/log"
)

// SubscribeUpstreamStatus reports every upstream of every chain on its own.
// minInterval is the minimum time between two responses about the same
// upstream; 0 sends every change at once.
func SubscribeUpstreamStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeUpstreamStatusServer,
	minInterval time.Duration,
) error {
	return SubscribeUpstreamStatusWithResync(upstreamSupervisor, stream, minInterval, defaultChainStateResyncInterval)
}

// SubscribeUpstreamStatusWithResync is SubscribeUpstreamStatus with a
// caller-chosen resync interval; tests use it to shrink the wait.
func SubscribeUpstreamStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeUpstreamStatusServer,
	minInterval, resyncInterval time.Duration,
) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	ctx, cancel := context.WithCancel(stream.Context())

	responses := make(chan *dshackle.SubscribeUpstreamStatusResponse, 100)
	followed := mapset.NewThreadUnsafeSet[chains.Chain]()
	var chainWorkers sync.WaitGroup
	// the chain goroutines unsubscribe from their feeds on the way out, so the
	// stream is over only when they are
	defer func() {
		cancel()
		chainWorkers.Wait()
	}()

	follow := func(chainSupervisor upstreams.ChainSupervisor) {
		if chainSupervisor == nil {
			return
		}
		chain := chainSupervisor.GetChain()
		if followed.ContainsOne(chain) {
			return
		}
		grpcId := chains.GetChain(chain.String()).GrpcId
		// a chain the emerald protocol has no number for cannot be reported
		if grpcId == 0 {
			return
		}
		feed, hasFeed := chainSupervisor.(upstreams.UpstreamStateFeed)
		if !hasFeed {
			return
		}
		followed.Add(chain)
		chainStatus := newUpstreamStatusChain(chain, grpcId, feed, minInterval, responses)
		chainWorkers.Go(func() {
			chainStatus.run(ctx, resyncInterval)
		})
	}

	// subscribed before the chains are listed, so a chain added in between is
	// in the list or in an event
	chainSupervisorEventsSub := upstreamSupervisor.SubscribeChainSupervisor("upstream_status")
	defer chainSupervisorEventsSub.Unsubscribe()
	chainSupervisorEvents := chainSupervisorEventsSub.Events

	for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
		follow(chainSupervisor)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chainSupervisorEvent, ok := <-chainSupervisorEvents:
			if !ok {
				chainSupervisorEvents = nil
				continue
			}
			if added, isAdd := chainSupervisorEvent.(*upstreams.AddChainSupervisorEvent); isAdd {
				follow(added.ChainSupervisor)
			}
		case response := <-responses:
			if err := stream.Send(response); err != nil {
				log.Error().Err(err).Msgf("failed to send a SubscribeUpstreamStatusResponse")
				return err
			}
		}
	}
}
