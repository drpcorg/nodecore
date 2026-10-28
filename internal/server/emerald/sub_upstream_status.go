package emerald

import (
	"context"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	specs "github.com/drpcorg/public/pkg/methods"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"maps"
	"math/rand/v2"
	"slices"
)

func (producer *upstreamStatusProducer) subscribe(upstreamSupervisor upstreams.UpstreamSupervisor, parent context.Context) error {
	if producer.interval <= 0 {
		producer.interval = config.DefaultGrpcUpstreamStatusInterval
	}
	ctx, cancel := context.WithCancel(parent)
	producer.cancel = cancel

	// a new chain supervisor only triggers a look at them all, so a dropped
	// event loses nothing
	chainSupervisorEvents := upstreamSupervisor.SubscribeChainSupervisor(fmt.Sprintf("upstream_status_%s", uuid.NewString()))
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
			chain := producer.newChain(chainSupervisor.GetChain())
			wg.Go(func() { producer.run(ctx, chainSupervisor, chain) })
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

func upstreamStatusInterval(appConfig *config.AppConfig) time.Duration {
	if appConfig == nil || appConfig.ServerConfig == nil {
		return config.DefaultGrpcUpstreamStatusInterval
	}
	return appConfig.ServerConfig.GrpcUpstreamStatusInterval
}

type upstreamStatusProducer struct {
	stream                   dshackle.Blockchain_SubscribeUpstreamStatusServer
	cancel                   context.CancelFunc
	interval, resyncInterval time.Duration
	sendMu                   sync.Mutex
	sendErr                  error
}

type upstreamStatusChain struct {
	ref       dshackle.ChainRef
	announced bool
	nextFull  time.Time
	sent      map[string]*protocol.UpstreamState
}

func (p *upstreamStatusProducer) newChain(chain chains.Chain) *upstreamStatusChain {
	return &upstreamStatusChain{ref: dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId), sent: make(map[string]*protocol.UpstreamState)}
}

// run sends a chain whenever its upstreams change or its resync is due, with
// at least interval between two sends.
func (p *upstreamStatusProducer) run(ctx context.Context, chainSupervisor upstreams.ChainSupervisor, chain *upstreamStatusChain) {
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
func (p *upstreamStatusProducer) send(ctx context.Context, chainSupervisor upstreams.ChainSupervisor, chain *upstreamStatusChain) (sent bool, ok bool) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if ctx.Err() != nil {
		return false, false
	}
	responses := chain.responses(chainSupervisor, time.Now(), p.resyncInterval)
	for _, response := range responses {
		if err := p.stream.Send(response); err != nil {
			log.Error().Err(err).Msg("failed to send upstream status")
			p.sendErr = err
			p.cancel()
			return false, false
		}
	}
	return len(responses) != 0, true
}

// SubscribeUpstreamStatusWithResync uses the chain-status response format, once
// per upstream. It shares the legacy full/delta/resync semantics.
func SubscribeUpstreamStatusWithResync(supervisor upstreams.UpstreamSupervisor, stream dshackle.Blockchain_SubscribeUpstreamStatusServer, interval, resync time.Duration) error {
	if supervisor == nil {
		return errNilUpstreamSupervisor
	}
	producer := &upstreamStatusProducer{stream: stream, interval: interval, resyncInterval: resync}
	return producer.subscribe(supervisor, stream.Context())
}

func (c *upstreamStatusChain) responses(supervisor upstreams.ChainSupervisor, now time.Time, resync time.Duration) []*dshackle.SubscribeChainStatusResponse {
	periodic := c.announced && !now.Before(c.nextFull)
	ids := slices.Clone(supervisor.GetUpstreamIds())
	slices.Sort(ids)
	present := make(map[string]bool, len(ids))
	var responses []*dshackle.SubscribeChainStatusResponse
	appendResponse := func(id, index string, response *dshackle.SubscribeChainStatusResponse) {
		response.UpstreamId, response.UpstreamIndex = id, index
		responses = append(responses, response)
	}
	for _, id := range ids {
		state := supervisor.GetUpstreamState(id)
		if state == nil {
			continue
		}
		present[id] = true
		previous := c.sent[id]
		if previous == nil {
			appendResponse(id, state.UpstreamIndex, toFullResponse(int(c.ref), upstreamChainState(supervisor.GetChain(), id, state)))
		} else {
			if periodic || upstreamDescriptionChanged(previous, state) {
				// As at network level, a resync repairs metadata separately from the head:
				// existing consumers treat a head-carrying response as a head-only delta.
				appendResponse(id, state.UpstreamIndex, stateWrappersToResponse(int(c.ref), snapshotStateWrappers(upstreamChainState(supervisor.GetChain(), id, state))))
			}
			if !previous.HeadData.Equals(state.HeadData) && !state.HeadData.IsEmptyByHeight() {
				appendResponse(id, state.UpstreamIndex, stateWrappersToResponse(int(c.ref), []upstreams.ChainSupervisorStateWrapper{upstreams.NewHeadWrapper(state.HeadData, id)}))
			}
		}
		c.sent[id] = state
	}
	// Retain unavailable tombstones for resync so a dropped removal is repaired.
	for _, id := range slices.Sorted(maps.Keys(c.sent)) {
		if present[id] {
			continue
		}
		previous := c.sent[id]
		if previous.Status != protocol.Unavailable || periodic {
			tombstone := protocol.DefaultUpstreamState(nil, nil, previous.UpstreamIndex, nil, nil)
			tombstone.Status = protocol.Unavailable
			c.sent[id] = &tombstone
			appendResponse(id, previous.UpstreamIndex, stateWrappersToResponse(int(c.ref), snapshotStateWrappers(upstreamChainState(supervisor.GetChain(), id, &tombstone))))
		}
	}
	if len(c.sent) > 0 && (!c.announced || periodic) {
		if c.announced {
			c.nextFull = now.Add(resync)
		} else {
			c.nextFull = now.Add(time.Duration(rand.Int64N(int64(max(resync, 1)))) + 1)
		}
		c.announced = true
	}
	return responses
}

func upstreamDescriptionChanged(a, b *protocol.UpstreamState) bool {
	return a.Status != b.Status || a.UpstreamIndex != b.UpstreamIndex || a.UpstreamMethods != b.UpstreamMethods || a.Caps != b.Caps || a.BlockInfo != b.BlockInfo || a.LowerBoundsInfo != b.LowerBoundsInfo || a.Labels != b.Labels
}

// upstreamChainState adapts one supervisor snapshot to the existing serializer.
// Status already includes network lag validation; no per-upstream lag is recomputed.
func upstreamChainState(chain chains.Chain, id string, state *protocol.UpstreamState) upstreams.ChainSupervisorState {
	callMethods := state.UpstreamMethods
	if callMethods == nil {
		callMethods = methods.NewChainMethods(nil)
	}
	caps := state.Caps
	if caps == nil {
		caps = mapset.NewThreadUnsafeSet[protocol.Cap]()
	}
	result := upstreams.ChainSupervisorState{Status: state.Status, HeadData: upstreams.NewChainHeadData(state.HeadData, id), Methods: callMethods, Caps: caps, LowerBounds: map[protocol.LowerBoundType]protocol.LowerBoundData{}}
	if state.BlockInfo != nil {
		result.Blocks = state.BlockInfo.GetBlocks()
	}
	if state.LowerBoundsInfo != nil {
		for _, bound := range state.LowerBoundsInfo.GetAllBounds() {
			result.LowerBounds[bound.Type] = bound
		}
	}
	if state.Labels != nil {
		result.ChainLabels = []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, state.Labels.GetAllLabels())}
	}
	result.SubMethods = upstreams.ProcessSubMethods(specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)), callMethods, caps)
	return result
}
