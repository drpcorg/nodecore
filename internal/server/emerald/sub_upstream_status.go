package emerald

import (
	"slices"
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
	"github.com/rs/zerolog/log"
)

// SubscribeUpstreamStatus streams the state of every upstream. It pulls: every
// interval it compares the upstreams of each chain with what the stream last
// sent and sends what differs, so nothing queues up or gets dropped and a slow
// consumer just gets fewer, fresher responses.
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
	producer := &upstreamStatusProducer{
		upstreamSupervisor: upstreamSupervisor,
		requested:          mapset.NewThreadUnsafeSet(request.GetChains()...),
		resyncInterval:     resyncInterval,
		chains:             make(map[chains.Chain]*upstreamStatusChain),
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for _, response := range producer.responses(time.Now()) {
			if err := stream.Send(response); err != nil {
				log.Error().Err(err).Msg("failed to send a SubscribeUpstreamStatusResponse")
				return err
			}
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
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
	upstreamSupervisor upstreams.UpstreamSupervisor
	requested          mapset.Set[dshackle.ChainRef]
	resyncInterval     time.Duration
	chains             map[chains.Chain]*upstreamStatusChain
}

// upstreamStatusChain is what the stream last sent for a chain.
type upstreamStatusChain struct {
	ref        dshackle.ChainRef
	wanted     bool
	subMethods mapset.Set[string]
	announced  bool
	lastFull   time.Time
	// the snapshots sent; the supervisor never mutates a stored snapshot
	sent    map[string]*protocol.UpstreamState
	current []upstreamSnapshot
}

type upstreamSnapshot struct {
	id    string
	state *protocol.UpstreamState
}

func (p *upstreamStatusProducer) responses(now time.Time) []*dshackle.SubscribeUpstreamStatusResponse {
	var responses []*dshackle.SubscribeUpstreamStatusResponse
	// re-read every tick, so chains added later appear
	for _, chainSupervisor := range p.upstreamSupervisor.GetChainSupervisors() {
		if chainSupervisor == nil {
			continue
		}
		if response := p.chainResponse(chainSupervisor, now); response != nil {
			responses = append(responses, response)
		}
	}
	return responses
}

func (p *upstreamStatusProducer) chainResponse(
	chainSupervisor upstreams.ChainSupervisor,
	now time.Time,
) *dshackle.SubscribeUpstreamStatusResponse {
	chain := p.chainOf(chainSupervisor.GetChain())
	if !chain.wanted {
		return nil
	}

	chain.current = chain.current[:0]
	for _, id := range chainSupervisor.GetUpstreamIds() {
		if state := chainSupervisor.GetUpstreamState(id); state != nil {
			chain.current = append(chain.current, upstreamSnapshot{id: id, state: state})
		}
	}

	full := false
	if !chain.announced {
		// a chain is introduced by its first upstream
		if len(chain.current) == 0 {
			return nil
		}
		full = true
	} else {
		full = now.Sub(chain.lastFull) >= p.resyncInterval
	}

	// a full response lists every upstream; a delta only the new or changed
	// ones, and the ids of those removed
	var upstreams []*dshackle.UpstreamStatus
	sent := make(map[string]*protocol.UpstreamState, len(chain.current))
	for _, snapshot := range chain.current {
		prev, known := chain.sent[snapshot.id]
		sent[snapshot.id] = snapshot.state
		if !full && known && !upstreamChanged(prev, snapshot.state) {
			continue
		}
		status := &dshackle.UpstreamStatus{
			UpstreamId: snapshot.id,
			Status:     ChainStatusToApi(snapshot.state.Status).GetStatus(),
		}
		if !snapshot.state.HeadData.IsEmptyByHeight() {
			status.Head = HeadToApi(snapshot.state.HeadData).GetHead()
		}
		if full || !known || descriptionChanged(prev, snapshot.state) {
			status.Description = upstreamDescription(snapshot.state, chain.subMethods)
		}
		upstreams = append(upstreams, status)
	}
	var removed []string
	if !full {
		for id := range chain.sent {
			if _, ok := sent[id]; !ok {
				removed = append(removed, id)
			}
		}
		slices.Sort(removed)
	}
	chain.sent = sent
	if !full && len(upstreams) == 0 && len(removed) == 0 {
		return nil
	}

	response := &dshackle.SubscribeUpstreamStatusResponse{
		Chain:              chain.ref,
		Upstreams:          upstreams,
		FullResponse:       full,
		RemovedUpstreamIds: removed,
	}
	if full {
		response.BuildInfo = &dshackle.BuildInfo{Version: buildinfo.ProductVersion()}
		chain.lastFull = now
	}
	chain.announced = true

	return response
}

func (p *upstreamStatusProducer) chainOf(chain chains.Chain) *upstreamStatusChain {
	if c, ok := p.chains[chain]; ok {
		return c
	}
	ref := dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId)
	c := &upstreamStatusChain{
		ref:        ref,
		wanted:     p.requested.Cardinality() == 0 || p.requested.ContainsOne(ref),
		subMethods: specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)),
	}
	p.chains[chain] = c
	return c
}

func upstreamChanged(prev, next *protocol.UpstreamState) bool {
	return prev != next && (!prev.HeadData.Equals(next.HeadData) || descriptionChanged(prev, next))
}

// descriptionChanged compares the copy-on-write parts by identity.
func descriptionChanged(prev, next *protocol.UpstreamState) bool {
	return prev.Status != next.Status ||
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
