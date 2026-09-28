package emerald

import (
	"context"
	"fmt"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	maxNodeGroupBatchItems = 1000
	// well under the 4 MiB a grpc client accepts by default
	maxNodeGroupBatchBytes = 1 << 20
)

type nodeGroupStatusStream interface {
	Send(*dshackle.SubscribeNodeGroupStatusResponse) error
	Context() context.Context
}

// SubscribeNodeGroupStatus streams the chain status per node group of the
// requested level, batched per window; a group turning unavailable is removed.
func SubscribeNodeGroupStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	request *dshackle.SubscribeNodeGroupStatusRequest,
	stream nodeGroupStatusStream,
	batchWindow time.Duration,
) error {
	return SubscribeNodeGroupStatusWithResync(upstreamSupervisor, request, stream, batchWindow, defaultChainStateResyncInterval)
}

// SubscribeNodeGroupStatusWithResync is SubscribeNodeGroupStatus with a
// caller-chosen state-resync interval; tests use it to shrink the wait.
func SubscribeNodeGroupStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	request *dshackle.SubscribeNodeGroupStatusRequest,
	stream nodeGroupStatusStream,
	batchWindow time.Duration,
	resyncInterval time.Duration,
) error {
	level, ok := upstreams.SeparationLevelFromProto(request.GetLevel())
	if !ok {
		return status.Errorf(codes.InvalidArgument, "unknown separation level %d", request.GetLevel())
	}
	return streamChainStatuses(
		stream.Context(),
		upstreamSupervisor,
		requestedChains(request.GetChains()),
		nodeGroupStatesProducer(level, resyncInterval),
		newNodeGroupStatusBatch(stream, level.Proto(), batchWindow),
	)
}

func nodeGroupBatchWindow(appConfig *config.AppConfig) time.Duration {
	if appConfig == nil || appConfig.ServerConfig == nil {
		return config.DefaultGrpcNodeGroupBatchWindow
	}
	return appConfig.ServerConfig.GrpcNodeGroupBatchWindow
}

// requestedChains filters by the chains of a request; none listed means all.
func requestedChains(refs []dshackle.ChainRef) func(chains.Chain) bool {
	if len(refs) == 0 {
		return allChains
	}
	requested := mapset.NewThreadUnsafeSet[chains.Chain]()
	for _, ref := range refs {
		requested.Add(chains.GetChainByGrpcId(int(ref)).Chain)
	}
	return requested.ContainsOne
}

func nodeGroupStatesProducer(level upstreams.SeparationLevel, resyncInterval time.Duration) chainStatusProducer {
	return func(
		ctx context.Context,
		chainSupervisor upstreams.ChainSupervisor,
		responses chan<- *dshackle.SubscribeChainStatusResponse,
	) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
		groupStatesSub := chainSupervisor.SubscribeNodeGroupStates(
			level,
			fmt.Sprintf("chain_supervisor_group_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
		)
		producer := &groupStatusProducer{
			ctx:             ctx,
			chainSupervisor: chainSupervisor,
			level:           level,
			grpcId:          chains.GetChain(chainSupervisor.GetChain().String()).GrpcId,
			responses:       responses,
			announced:       make(map[string]bool),
		}
		go producer.run(groupStatesSub.Events, resyncInterval)
		return groupStatesSub
	}
}

// groupStatusProducer turns the group events of one chain into responses.
type groupStatusProducer struct {
	ctx             context.Context
	chainSupervisor upstreams.ChainSupervisor
	level           upstreams.SeparationLevel
	grpcId          int
	responses       chan<- *dshackle.SubscribeChainStatusResponse
	// the groups the consumer holds; an unavailable status drops one, so it
	// comes back with a full
	announced map[string]bool
}

func (p *groupStatusProducer) run(events <-chan *upstreams.ChainSupervisorStateWrapperEvent, resyncInterval time.Duration) {
	if !p.sync() {
		return
	}

	resyncTicker := time.NewTicker(resyncInterval)
	defer resyncTicker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-resyncTicker.C:
			if !p.sync() {
				return
			}
		case event, ok := <-events:
			if !ok || !p.forward(event) {
				return
			}
		}
	}
}

func (p *groupStatusProducer) forward(event *upstreams.ChainSupervisorStateWrapperEvent) bool {
	if len(event.Wrappers) == 0 || event.NodeGroupId == "" {
		return true
	}
	if p.announced[event.NodeGroupId] {
		return p.sendDelta(event.NodeGroupId, event.Wrappers)
	}
	// a group that died before it was ever announced is skipped entirely
	state, exists := p.chainSupervisor.GetNodeGroupState(p.level, event.NodeGroupId)
	return !exists || p.announce(event.NodeGroupId, state)
}

// sync reconciles the consumer's view with the live groups: it announces new
// groups, snapshots the announced ones and tombstones announced groups that
// are gone, so a lost delta is repaired within one resync interval.
func (p *groupStatusProducer) sync() bool {
	live := p.chainSupervisor.GetNodeGroupStates(p.level)
	for nodeGroupId := range p.announced {
		if _, ok := live[nodeGroupId]; !ok {
			tombstone := []upstreams.ChainSupervisorStateWrapper{upstreams.NewStatusWrapper(protocol.Unavailable)}
			if !p.sendDelta(nodeGroupId, tombstone) {
				return false
			}
		}
	}
	for nodeGroupId, state := range live {
		if !p.syncGroup(nodeGroupId, state) {
			return false
		}
	}
	return true
}

func (p *groupStatusProducer) syncGroup(nodeGroupId string, state upstreams.ChainSupervisorState) bool {
	if p.announced[nodeGroupId] {
		return p.sendDelta(nodeGroupId, snapshotStateWrappers(state))
	}
	return p.announce(nodeGroupId, state)
}

// announce introduces a group with a full. Head-gated like the network stream;
// for consumers Unavailable means removed, so an unavailable group is
// introduced once it recovers.
func (p *groupStatusProducer) announce(nodeGroupId string, state upstreams.ChainSupervisorState) bool {
	if state.HeadData.IsEmpty() || state.Status == protocol.Unavailable {
		return true
	}
	if !sendResponse(p.ctx, p.responses, toFullResponse(p.grpcId, nodeGroupId, state)) {
		return false
	}
	p.announced[nodeGroupId] = true
	return true
}

// sendDelta forwards a delta of an announced group; an Unavailable status
// removes the group on the consumer.
func (p *groupStatusProducer) sendDelta(nodeGroupId string, wrappers []upstreams.ChainSupervisorStateWrapper) bool {
	if !sendResponse(p.ctx, p.responses, stateWrappersToResponse(p.grpcId, nodeGroupId, wrappers)) {
		return false
	}
	if hasUnavailableStatus(wrappers) {
		delete(p.announced, nodeGroupId)
	}
	return true
}

func hasUnavailableStatus(wrappers []upstreams.ChainSupervisorStateWrapper) bool {
	for _, wrapper := range wrappers {
		if statusWrapper, ok := wrapper.(*upstreams.StatusWrapper); ok && statusWrapper.Status == protocol.Unavailable {
			return true
		}
	}
	return false
}

// nodeGroupStatusBatch collects the responses of one flush window. Heads of a
// group conflate to the latest; fulls and deltas keep their order and are
// never dropped, and a head of a group whose full is pending merges into it.
type nodeGroupStatusBatch struct {
	stream nodeGroupStatusStream
	level  dshackle.SeparationLevel
	window time.Duration
	timer  *time.Timer // runs while the batch is not empty
	items  []*dshackle.SubscribeChainStatusResponse
	bytes  int
	// the item carrying a group's latest head: a head-only delta or a full
	headSlots map[nodeGroupKey]int
}

type nodeGroupKey struct {
	chain dshackle.ChainRef
	id    string
}

func newNodeGroupStatusBatch(stream nodeGroupStatusStream, level dshackle.SeparationLevel, window time.Duration) *nodeGroupStatusBatch {
	if window <= 0 {
		window = config.DefaultGrpcNodeGroupBatchWindow
	}
	return &nodeGroupStatusBatch{
		stream:    stream,
		level:     level,
		window:    window,
		headSlots: make(map[nodeGroupKey]int),
	}
}

func (b *nodeGroupStatusBatch) write(response *dshackle.SubscribeChainStatusResponse) error {
	b.add(response)
	if len(b.items) >= maxNodeGroupBatchItems || b.bytes >= maxNodeGroupBatchBytes {
		return b.flush()
	}
	if b.timer == nil {
		b.timer = time.NewTimer(b.window)
	}
	return nil
}

func (b *nodeGroupStatusBatch) flushes() <-chan time.Time {
	if b.timer == nil {
		return nil
	}
	return b.timer.C
}

func (b *nodeGroupStatusBatch) flush() error {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.items) == 0 {
		return nil
	}
	response := &dshackle.SubscribeNodeGroupStatusResponse{Items: b.items, Level: b.level}
	b.items, b.bytes = nil, 0
	clear(b.headSlots)
	if err := b.stream.Send(response); err != nil {
		log.Error().Err(err).Msgf("failed to send a SubscribeNodeGroupStatusResponse")
		return err
	}
	return nil
}

func (b *nodeGroupStatusBatch) add(response *dshackle.SubscribeChainStatusResponse) {
	description := response.GetChainDescription()
	key := nodeGroupKey{chain: description.GetChain(), id: description.GetNodeGroupId()}
	head := headOnlyEvent(response)
	if head != nil {
		if slot, ok := b.headSlots[key]; ok {
			// a replaced head changes the batch size by a few bytes at most
			setHead(b.items[slot], head)
			return
		}
	}
	b.items = append(b.items, response)
	b.bytes += proto.Size(response)
	if head != nil || response.FullResponse {
		b.headSlots[key] = len(b.items) - 1
	}
}

// headOnlyEvent returns the head of a delta that carries nothing else.
func headOnlyEvent(response *dshackle.SubscribeChainStatusResponse) *dshackle.ChainEvent {
	events := response.GetChainDescription().GetChainEvent()
	if response.FullResponse || len(events) != 1 || events[0].GetHead() == nil {
		return nil
	}
	return events[0]
}

func setHead(response *dshackle.SubscribeChainStatusResponse, head *dshackle.ChainEvent) {
	events := response.GetChainDescription().GetChainEvent()
	for i, event := range events {
		if event.GetHead() != nil {
			events[i] = head
			return
		}
	}
}
