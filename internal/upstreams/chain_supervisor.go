package upstreams

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var availabilityMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "availability_status",
		Help:      "Current availability status of the upstream: 1 = available, 2 = immature, 3 = syncing, 4 = unavailable",
	},
	[]string{"chain", "upstream"},
)

func init() {
	prometheus.MustRegister(availabilityMetric)
}

type BaseChainSupervisor struct {
	ctx             context.Context
	chain           chains.Chain
	fc              choice.ForkChoice
	state           *utils.Atomic[ChainSupervisorState]
	eventsChan      chan protocol.UpstreamEvent
	upstreamStates  *utils.CMap[string, *protocol.UpstreamState]
	tracker         dimensions.DimensionTracker
	subChainMethods mapset.Set[string]

	validateLag bool
	syncingLag  int64
	getUpstream func(string) Upstream
	lastOver    map[string]bool

	roundRobinIndex atomic.Uint64

	subStateManager *utils.SubscriptionManager[*ChainSupervisorStateWrapperEvent]

	// node-group tracking (Separation mode). groups and upstreamGroup are
	// mutated only from the processEvents goroutine (same ownership model as
	// lastOver); the CMap and the per-group Atomic exist for cross-goroutine
	// reads by the chain-status stream.
	groups               *utils.CMap[string, *groupState] // keyed by GroupKey.Id()
	upstreamGroup        map[string]groupMembership
	subGroupStateManager *utils.SubscriptionManager[*ChainSupervisorStateWrapperEvent]
}

func NewBaseChainSupervisor(
	ctx context.Context,
	chain chains.Chain,
	fc choice.ForkChoice,
	tracker dimensions.DimensionTracker,
	validateLag bool,
	getUpstream func(string) Upstream,
) *BaseChainSupervisor {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(initialChainSupervisorState())

	return &BaseChainSupervisor{
		ctx:                  ctx,
		tracker:              tracker,
		chain:                chain,
		fc:                   fc,
		eventsChan:           make(chan protocol.UpstreamEvent, 100),
		upstreamStates:       utils.NewCMap[string, *protocol.UpstreamState](),
		state:                state,
		subChainMethods:      specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)),
		validateLag:          validateLag,
		syncingLag:           chains.GetChain(chain.String()).Settings.Lags.Syncing,
		getUpstream:          getUpstream,
		lastOver:             make(map[string]bool),
		subStateManager:      utils.NewSubscriptionManager[*ChainSupervisorStateWrapperEvent]("chain_supervisor_events"),
		groups:               utils.NewCMap[string, *groupState](),
		upstreamGroup:        make(map[string]groupMembership),
		subGroupStateManager: utils.NewSubscriptionManager[*ChainSupervisorStateWrapperEvent]("chain_supervisor_group_events"),
	}
}

func initialChainSupervisorState() ChainSupervisorState {
	return ChainSupervisorState{
		Status:      protocol.Available,
		Blocks:      make(map[protocol.BlockType]protocol.Block),
		LowerBounds: make(map[protocol.LowerBoundType]protocol.LowerBoundData),
		HeadData:    NewChainHeadData(protocol.ZeroBlock{}, ""),
		Methods:     methods.NewChainMethods(nil),
		ChainLabels: make([]AggregatedLabels, 0),
		SubMethods:  mapset.NewThreadUnsafeSet[string](),
		Caps:        mapset.NewThreadUnsafeSet[protocol.Cap](),
	}
}

func (b *BaseChainSupervisor) GetChain() chains.Chain {
	return b.chain
}

func (b *BaseChainSupervisor) Start() {
	go b.processEvents()

	go func() {
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}

			b.monitor()
		}
	}()
}

func (b *BaseChainSupervisor) GetChainState() ChainSupervisorState {
	return b.state.Load()
}

func (b *BaseChainSupervisor) GetMethod(methodName string) *specs.Method {
	return b.GetChainState().Methods.GetMethod(methodName)
}

func (b *BaseChainSupervisor) GetMethods() []string {
	if b.GetChainState().Methods == nil {
		return nil
	}
	return b.GetChainState().Methods.GetSupportedMethods().ToSlice()
}

func (b *BaseChainSupervisor) PublishUpstreamEvent(event protocol.UpstreamEvent) {
	b.eventsChan <- event
}

func (b *BaseChainSupervisor) SubscribeState(name string) *utils.Subscription[*ChainSupervisorStateWrapperEvent] {
	return b.subStateManager.Subscribe(name)
}

func (b *BaseChainSupervisor) GetUpstreamState(upstreamId string) *protocol.UpstreamState {
	if s, ok := b.upstreamStates.Load(upstreamId); ok {
		return s
	}
	return nil
}

func (b *BaseChainSupervisor) GetSortedUpstreamIds(filterFunc FilterUpstream, sortFunc SortUpstream) []string {
	entries := make([]lo.Tuple2[string, *protocol.UpstreamState], 0)
	b.upstreamStates.Range(func(upId string, state *protocol.UpstreamState) bool {
		if filterFunc(upId, state) {
			entries = append(entries, lo.T2(upId, state))
		}
		return true
	})
	slices.SortFunc(entries, sortFunc)

	return lo.Map(entries, func(item lo.Tuple2[string, *protocol.UpstreamState], index int) string {
		return item.A
	})
}

func (b *BaseChainSupervisor) GetUpstreamIds() []string {
	ids := make([]string, 0)
	b.upstreamStates.Range(func(upId string, _ *protocol.UpstreamState) bool {
		ids = append(ids, upId)
		return true
	})
	slices.Sort(ids)
	return ids
}

func (b *BaseChainSupervisor) NextIndex() uint64 {
	return b.roundRobinIndex.Add(1)
}

func (b *BaseChainSupervisor) processEvents() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case event, ok := <-b.eventsChan:
			if ok {
				switch eventType := event.EventType.(type) {
				case *protocol.RemoveUpstreamEvent:
					if upState, upOk := b.upstreamStates.Load(event.Id); upOk {
						upHead := upState.HeadData
						b.upstreamStates.Delete(event.Id)
						delete(b.lastOver, event.Id)

						b.updateState()
						b.updateHead(event.Id, &protocol.HeadUpstreamEvent{Status: protocol.Unavailable, Head: upHead})
						b.removeFromGroup(event.Id)
					}
				case *protocol.HeadUpstreamEvent:
					// Keep the per-upstream snapshot's head fresh - head updates
					// arrive as HeadUpstreamEvent (not StateUpstreamEvent), so
					// without this the head read by selection matchers and head-lag
					// tracking would stay frozen at the last StateUpstreamEvent.
					// Copy-on-write: matchers read the stored pointer concurrently.
					if upState, upOk := b.upstreamStates.Load(event.Id); upOk {
						newUpState := *upState
						newUpState.HeadData = eventType.Head
						b.upstreamStates.Store(event.Id, &newUpState)
					}
					b.updateHead(event.Id, eventType)
					b.updateGroupHead(event.Id, eventType)
				case *protocol.StateUpstreamEvent:
					availabilityMetric.WithLabelValues(b.chain.String(), event.Id).Set(float64(eventType.State.Status))
					b.upstreamStates.Store(event.Id, eventType.State)
					b.updateState()
					b.assignGroup(event.Id, eventType.State)
				case *protocol.ValidUpstreamEvent:
					// Symmetric to RemoveUpstreamEvent: a recovered upstream is
					// re-registered right away from the event's state snapshot.
					// Relying on a later StateUpstreamEvent instead would leave a
					// node that came back unchanged out of the chain forever -
					// those events are suppressed unless some sub-state differs.
					if eventType.State != nil {
						availabilityMetric.WithLabelValues(b.chain.String(), event.Id).Set(float64(eventType.State.Status))
						b.upstreamStates.Store(event.Id, eventType.State)
						b.updateState()
						if !eventType.State.HeadData.IsEmptyByHeight() {
							b.updateHead(event.Id, &protocol.HeadUpstreamEvent{Status: eventType.State.Status, Head: eventType.State.HeadData})
						}
						b.assignGroup(event.Id, eventType.State)
					}
				}
			}
		}
	}
}

func (b *BaseChainSupervisor) updateHead(upstreamId string, headEvent *protocol.HeadUpstreamEvent) {
	newState := b.state.Load()
	var headWrapper *ChainSupervisorStateWrapperEvent
	if headEvent != nil && !headEvent.Head.IsEmptyByHeight() {
		updated, head := b.fc.Choose(upstreamId, headEvent)
		if updated {
			newState.HeadData = NewChainHeadData(head, upstreamId)
			if !newState.HeadData.IsEmpty() {
				headWrapper = &ChainSupervisorStateWrapperEvent{
					Wrappers: []ChainSupervisorStateWrapper{NewHeadWrapper(newState.HeadData.Head, upstreamId)},
				}
			}
		}
	} else if headEvent != nil {
		newState.HeadData = NewChainHeadData(protocol.ZeroBlock{}, upstreamId)
	}

	b.state.Store(newState)
	if headWrapper != nil {
		b.subStateManager.Publish(headWrapper)
	}
	b.calculateHeadLags()
}

func (b *BaseChainSupervisor) updateState() {
	currentState := b.state.Load()
	// recomputeState merges only available upstreams; the status is the min over all
	newState := recomputeState(currentState, b.allUpstreamStates(), b.subChainMethods)

	eventWrappers := currentState.Compare(newState)
	b.state.Store(newState)
	if len(eventWrappers) > 0 {
		b.subStateManager.Publish(&ChainSupervisorStateWrapperEvent{Wrappers: eventWrappers})
	}
	b.calculateFinalizationLags()
}

func (b *BaseChainSupervisor) calculateFinalizationLags() {
	if b.tracker != nil {
		state := b.state.Load()

		b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
			finalizationBlock, ok := state.Blocks[protocol.FinalizedBlock]
			finalizationLag := uint64(0)
			if ok && !finalizationBlock.IsEmptyByHeight() {
				upFinalized := val.BlockInfo.GetBlock(protocol.FinalizedBlock)
				if !upFinalized.IsEmptyByHeight() && finalizationBlock.Height >= upFinalized.Height {
					finalizationLag = finalizationBlock.Height - upFinalized.Height
				}
			}
			b.tracker.GetChainDimensions(b.chain, key).TrackFinalizationLag(finalizationLag)

			return true
		})
	}
}

func (b *BaseChainSupervisor) calculateHeadLags() {
	state := b.state.Load()

	b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		var headLag uint64
		if state.HeadData.Head.Height >= val.HeadData.Height {
			headLag = state.HeadData.Head.Height - val.HeadData.Height
		}
		if b.tracker != nil {
			b.tracker.GetChainDimensions(b.chain, key).TrackHeadLag(headLag)
		}

		if b.validateLag && b.getUpstream != nil {
			// Push a lag update only when the upstream crosses the syncing
			// threshold (over -> under or under -> over), not on every per-block
			// lag change. The derived status only flips at that boundary, so this
			// bounds emissions to ~2 per syncing episode and keeps the upstream
			// emitter off the per-block hot path.
			lag := int64(headLag)
			over := protocol.LagExceeds(lag, b.syncingLag)
			if b.lastOver[key] != over {
				if up := b.getUpstream(key); up != nil {
					b.lastOver[key] = over
					up.UpdateHeadLag(lag)
				}
			}
		}
		return true
	})
}

func (b *BaseChainSupervisor) allUpstreamStates() []*protocol.UpstreamState {
	states := make([]*protocol.UpstreamState, 0)

	b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		states = append(states, val)
		return true
	})

	return states
}

func (b *BaseChainSupervisor) monitor() {
	state := b.state.Load()

	var height string
	if state.HeadData.Head.Height > 0 {
		height = fmt.Sprintf("%d", state.HeadData.Head.Height)
	} else {
		height = "?"
	}

	statuses := make(map[protocol.AvailabilityStatus]int)
	b.upstreamStates.Range(func(key string, upState *protocol.UpstreamState) bool {
		statuses[upState.Status]++

		return true
	})
	boundsSlice := lo.MapToSlice(state.LowerBounds, func(key protocol.LowerBoundType, val protocol.LowerBoundData) string {
		return fmt.Sprintf("%s=%d", key, val.Bound)
	})
	bounds := strings.Join(boundsSlice, ", ")

	upstreamStatuses, weakUpstreams := b.getStatuses()

	log.Info().Msgf(
		"State of %s: height=%s, statuses=[%s], bounds=[%s], weak=[%s]",
		strings.ToUpper(b.chain.String()),
		height,
		upstreamStatuses,
		bounds,
		weakUpstreams,
	)
}

func (b *BaseChainSupervisor) getStatuses() (string, string) {
	statuses := make(map[protocol.AvailabilityStatus]int)
	weakUpstreams := make([]string, 0)
	b.upstreamStates.Range(func(upId string, upState *protocol.UpstreamState) bool {
		statuses[upState.Status]++
		if upState.Status != protocol.Available {
			weakUpstreams = append(weakUpstreams, upId)
		}

		return true
	})

	if len(statuses) == 0 {
		return "", ""
	}
	statusPairs := make([]string, 0)
	for key, value := range statuses {
		statusPairs = append(statusPairs, fmt.Sprintf("%s/%d", key, value))
	}

	return strings.Join(statusPairs, ", "), strings.Join(weakUpstreams, ", ")
}

var _ ChainSupervisor = (*BaseChainSupervisor)(nil)
