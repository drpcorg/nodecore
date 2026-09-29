package emerald

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// SubscribeUpstreamStatus streams the state of every upstream. A chain is sent
// when the chain supervisor signals a change of its upstreams, at most once per
// interval, and what is sent is read at that moment: nothing queues up or gets
// dropped, and a slow consumer just gets fewer, fresher responses.
func SubscribeUpstreamStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	request *dshackle.SubscribeUpstreamStatusRequest,
	stream dshackle.Blockchain_SubscribeUpstreamStatusServer,
	interval time.Duration,
) error {
	return SubscribeUpstreamStatusWithResync(upstreamSupervisor, request, stream, interval, defaultChainStateResyncInterval)
}

// SubscribeUpstreamStatusWithResync is SubscribeUpstreamStatus with a
// caller-chosen resync interval; tests use it to shrink the wait.
func SubscribeUpstreamStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	request *dshackle.SubscribeUpstreamStatusRequest,
	stream dshackle.Blockchain_SubscribeUpstreamStatusServer,
	interval time.Duration,
	resyncInterval time.Duration,
) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	if interval <= 0 {
		interval = config.DefaultGrpcUpstreamStatusInterval
	}
	ctx, cancel := context.WithCancel(stream.Context())
	producer := &upstreamStatusProducer{
		stream:         stream,
		cancel:         cancel,
		requested:      mapset.NewThreadUnsafeSet(request.GetChains()...),
		interval:       interval,
		resyncInterval: resyncInterval,
	}

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

func upstreamStatusInterval(appConfig *config.AppConfig) time.Duration {
	if appConfig == nil || appConfig.ServerConfig == nil {
		return config.DefaultGrpcUpstreamStatusInterval
	}
	return appConfig.ServerConfig.GrpcUpstreamStatusInterval
}

type upstreamStatusProducer struct {
	stream         dshackle.Blockchain_SubscribeUpstreamStatusServer
	cancel         context.CancelFunc
	requested      mapset.Set[dshackle.ChainRef]
	interval       time.Duration
	resyncInterval time.Duration

	// serializes the chains' sends; each response is built under it, from the
	// state of that moment
	sendMu  sync.Mutex
	sendErr error
}

// upstreamStatusChain is what the stream last sent for a chain.
type upstreamStatusChain struct {
	ref        dshackle.ChainRef
	subMethods mapset.Set[string]
	announced  bool
	nextFull   time.Time
	// the snapshots sent (the supervisor never mutates a stored one), each
	// with the pass that last saw it
	sent map[string]sentUpstream
	pass uint64
}

type sentUpstream struct {
	state *protocol.UpstreamState
	pass  uint64
}

func (p *upstreamStatusProducer) newChain(chain chains.Chain) *upstreamStatusChain {
	ref := dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId)
	if p.requested.Cardinality() > 0 && !p.requested.ContainsOne(ref) {
		return nil
	}
	return &upstreamStatusChain{
		ref:        ref,
		subMethods: specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)),
		sent:       make(map[string]sentUpstream),
	}
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
	response := chain.response(chainSupervisor, time.Now(), p.resyncInterval)
	if response == nil {
		return false, true
	}
	if err := p.stream.Send(response); err != nil {
		log.Error().Err(err).Msg("failed to send a SubscribeUpstreamStatusResponse")
		p.sendErr = err
		p.cancel()
		return false, false
	}
	return true, true
}

func (c *upstreamStatusChain) response(
	chainSupervisor upstreams.ChainSupervisor,
	now time.Time,
	resyncInterval time.Duration,
) *dshackle.SubscribeUpstreamStatusResponse {
	full := !c.announced || !now.Before(c.nextFull)
	c.pass++

	// a full response lists every upstream; a delta only the new or changed
	// ones, and the ids of those removed
	var upstreamStatuses []*dshackle.UpstreamStatus
	present := 0
	for _, id := range chainSupervisor.GetUpstreamIds() {
		state := chainSupervisor.GetUpstreamState(id)
		if state == nil {
			continue
		}
		present++
		prev, known := c.sent[id]
		c.sent[id] = sentUpstream{state: state, pass: c.pass}
		if !full && known && !upstreamChanged(prev.state, state) {
			continue
		}
		status := &dshackle.UpstreamStatus{
			UpstreamId:    id,
			UpstreamIndex: state.UpstreamIndex,
			Status:        ChainStatusToApi(state.Status).GetStatus(),
		}
		if !state.HeadData.IsEmptyByHeight() {
			status.Head = HeadToApi(state.HeadData).GetHead()
		}
		if full || !known || descriptionChanged(prev.state, state) {
			status.Description = upstreamDescription(state, c.subMethods)
		}
		upstreamStatuses = append(upstreamStatuses, status)
	}
	// a chain is introduced by its first upstream
	if !c.announced && present == 0 {
		return nil
	}
	var removed []string
	if len(c.sent) > present {
		for id, sent := range c.sent {
			if sent.pass != c.pass {
				delete(c.sent, id)
				removed = append(removed, id)
			}
		}
		slices.Sort(removed)
	}
	if full {
		removed = nil
	} else if len(upstreamStatuses) == 0 && len(removed) == 0 {
		return nil
	}

	response := &dshackle.SubscribeUpstreamStatusResponse{
		Chain:              c.ref,
		Upstreams:          upstreamStatuses,
		FullResponse:       full,
		RemovedUpstreamIds: removed,
	}
	if full {
		response.BuildInfo = &dshackle.BuildInfo{Version: buildinfo.ProductVersion()}
		if c.announced {
			c.nextFull = now.Add(resyncInterval)
		} else {
			// the first resync of each chain at a random offset, so the fulls
			// of all chains do not come at once
			c.nextFull = now.Add(time.Duration(rand.Int64N(int64(max(resyncInterval, 1)))) + 1)
		}
	}
	c.announced = true

	return response
}

func upstreamChanged(prev, next *protocol.UpstreamState) bool {
	return prev != next && (!prev.HeadData.Equals(next.HeadData) || descriptionChanged(prev, next))
}

// descriptionChanged compares the copy-on-write parts by identity.
func descriptionChanged(prev, next *protocol.UpstreamState) bool {
	return prev.UpstreamIndex != next.UpstreamIndex ||
		prev.Status != next.Status ||
		prev.UpstreamMethods != next.UpstreamMethods ||
		prev.Caps != next.Caps ||
		prev.BlockInfo != next.BlockInfo ||
		prev.LowerBoundsInfo != next.LowerBoundsInfo ||
		prev.Labels != next.Labels
}

// upstreamDescription is what the merged full response describes, for this
// upstream alone and whatever its status; status and head have their own fields.
func upstreamDescription(state *protocol.UpstreamState, subChainMethods mapset.Set[string]) []*dshackle.ChainEvent {
	upstreamMethods := state.UpstreamMethods
	if upstreamMethods == nil {
		upstreamMethods = methods.NewChainMethods(nil)
	}
	var lowerBounds []protocol.LowerBoundData
	if state.LowerBoundsInfo != nil {
		lowerBounds = state.LowerBoundsInfo.GetAllBounds()
	}
	var blocks map[protocol.BlockType]protocol.Block
	if state.BlockInfo != nil {
		blocks = state.BlockInfo.GetBlocks()
	}
	var labels map[string]string
	if state.Labels != nil {
		labels = state.Labels.GetAllLabels()
	}

	return []*dshackle.ChainEvent{
		SupportedMethodsToApi(upstreamMethods.GetSupportedMethods().ToSlice()),
		LowerBoundsToApi(lowerBounds),
		BlocksToApi(blocks),
		SubMethodsToApi(upstreams.ProcessSubMethods(subChainMethods, upstreamMethods, state.Caps).ToSlice()),
		LabelsToApi([]upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, labels)}),
	}
}
