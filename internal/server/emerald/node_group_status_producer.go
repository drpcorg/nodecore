package emerald

import (
	"context"
	"fmt"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func (producer *nodeGroupStatusProducer) subscribe(upstreamSupervisor upstreams.UpstreamSupervisor, parent context.Context) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	if producer.interval <= 0 {
		producer.interval = config.DefaultGrpcUpstreamStatusInterval
	}
	ctx, cancel := context.WithCancel(parent)
	producer.cancel = cancel

	// a new chain supervisor only triggers a look at them all, so a dropped
	// event loses nothing
	chainSupervisorEvents := upstreamSupervisor.SubscribeChainSupervisor(fmt.Sprintf("node_group_status_%s", uuid.NewString()))
	defer chainSupervisorEvents.Unsubscribe()
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	started := make(map[chains.Chain]bool)
	startChains := func() {
		for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
			if chainSupervisor == nil || started[chainSupervisor.GetChain()] {
				continue
			}
			started[chainSupervisor.GetChain()] = true
			if chain := producer.newChain(chainSupervisor.GetChain()); chain != nil {
				wg.Go(func() { producer.run(ctx, chainSupervisor, chain) })
			}
		}
	}
	startChains()
	events := chainSupervisorEvents.Events
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return producer.sendErr
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			startChains()
		}
	}
}

func nodeGroupStatusInterval(appConfig *config.AppConfig) time.Duration {
	if appConfig == nil || appConfig.ServerConfig == nil {
		return config.DefaultGrpcUpstreamStatusInterval
	}
	return appConfig.ServerConfig.GrpcUpstreamStatusInterval
}

type nodeGroupStatusProducer struct {
	groupStream    dshackle.Blockchain_SubscribeNodeGroupStatusServer
	fullSeparation bool
	compactUpdates bool
	cancel         context.CancelFunc
	requested      mapset.Set[dshackle.ChainRef]
	interval       time.Duration
	resyncInterval time.Duration

	// serializes the chains' sends; each response is built under it, from the
	// state of that moment
	sendMu  sync.Mutex
	sendErr error
}

// nodeGroupStatusChain is what the stream last sent for a chain.
type nodeGroupStatusChain struct {
	compactUpdates bool
	groupTracker   upstreams.NodeGroupTracker
	sentNetwork    *dshackle.ChainDescription
	sentGroups     map[string]*dshackle.NodeGroupStatus
	ref            dshackle.ChainRef
	announced      bool
	nextFull       time.Time
}

func (p *nodeGroupStatusProducer) newChain(chain chains.Chain) *nodeGroupStatusChain {
	ref := dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId)
	if p.requested.Cardinality() > 0 && !p.requested.ContainsOne(ref) {
		return nil
	}
	return &nodeGroupStatusChain{
		ref:            ref,
		compactUpdates: p.compactUpdates,
	}
}

// run sends a chain whenever its upstreams change or its resync is due, with
// at least interval between two sends.
func (p *nodeGroupStatusProducer) run(ctx context.Context, chainSupervisor upstreams.ChainSupervisor, chain *nodeGroupStatusChain) {
	resync := time.NewTimer(time.Hour)
	resync.Stop()
	defer resync.Stop()
	gap := time.NewTimer(time.Hour)
	gap.Stop()
	defer gap.Stop()

	var lastSent time.Time
	for {
		// taken before the state is read, so a later change wakes the loop
		changed := chainSupervisor.UpstreamsChanged()
		sent, ok := p.send(ctx, chainSupervisor, chain)
		if !ok {
			return
		}
		if sent {
			lastSent = time.Now()
		}
		if chain.announced {
			resync.Reset(time.Until(chain.nextFull))
		}

		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-resync.C:
		}
		if wait := time.Until(lastSent.Add(p.interval)); wait > 0 {
			gap.Reset(wait)
			select {
			case <-ctx.Done():
				return
			case <-gap.C:
			}
		}
	}
}

// send sends what differs from the chain's last response, if anything; ok is
// false once the stream is done.
func (p *nodeGroupStatusProducer) send(ctx context.Context, chainSupervisor upstreams.ChainSupervisor, chain *nodeGroupStatusChain) (sent bool, ok bool) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if ctx.Err() != nil {
		return false, false
	}
	response := chain.groupResponse(chainSupervisor, time.Now(), p.resyncInterval, p.fullSeparation)
	if response == nil {
		return false, true
	}
	err := p.groupStream.Send(response)
	if err != nil {
		log.Error().Err(err).Msg("failed to send nodecore status")
		p.sendErr = err
		p.cancel()
		return false, false
	}
	return true, true
}
