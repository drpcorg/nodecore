package upstreams

import (
	"context"
	"fmt"
	"slices"
	"strings"
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

	subStateManager *utils.SubscriptionManager[*ChainSupervisorStateWrapperEvent]
}

func NewBaseChainSupervisor(ctx context.Context, chain chains.Chain, fc choice.ForkChoice, tracker dimensions.DimensionTracker) *BaseChainSupervisor {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(
		ChainSupervisorState{
			Status:      protocol.Available,
			Blocks:      make(map[protocol.BlockType]protocol.Block),
			LowerBounds: make(map[protocol.LowerBoundType]protocol.LowerBoundData),
			HeadData:    NewChainHeadData(protocol.ZeroBlock{}, ""),
			Methods:     methods.NewChainMethods(nil),
			ChainLabels: make([]AggregatedLabels, 0),
			SubMethods:  mapset.NewThreadUnsafeSet[string](),
		},
	)

	return &BaseChainSupervisor{
		ctx:             ctx,
		tracker:         tracker,
		chain:           chain,
		fc:              fc,
		eventsChan:      make(chan protocol.UpstreamEvent, 100),
		upstreamStates:  utils.NewCMap[string, *protocol.UpstreamState](),
		state:           state,
		subChainMethods: specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)),
		subStateManager: utils.NewSubscriptionManager[*ChainSupervisorStateWrapperEvent]("chain_supervisor_events"),
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

						b.updateState()
						b.updateHead(event.Id, &protocol.HeadUpstreamEvent{Status: protocol.Unavailable, Head: upHead})
					}
				case *protocol.HeadUpstreamEvent:
					b.updateHead(event.Id, eventType)
				case *protocol.StateUpstreamEvent:
					availabilityMetric.WithLabelValues(b.chain.String(), event.Id).Set(float64(eventType.State.Status))
					b.upstreamStates.Store(event.Id, eventType.State)
					b.updateState()
				}
			}
		}
	}
}

func (b *BaseChainSupervisor) updateHead(upstreamId string, headEvent *protocol.HeadUpstreamEvent) {
	newState := b.state.Load()
	if headEvent != nil && !headEvent.Head.IsEmptyByHeight() {
		updated, head := b.fc.Choose(upstreamId, headEvent)
		if updated {
			newState.HeadData = NewChainHeadData(head, upstreamId)

			if !newState.HeadData.IsEmpty() {
				b.subStateManager.Publish(
					&ChainSupervisorStateWrapperEvent{
						[]ChainSupervisorStateWrapper{NewHeadWrapper(newState.HeadData.Head)},
					},
				)
			}
		}
	} else if headEvent != nil {
		newState.HeadData = NewChainHeadData(protocol.ZeroBlock{}, upstreamId)
	}

	b.state.Store(newState)
	b.calculateHeadLags()
}

func (b *BaseChainSupervisor) updateState() {
	currentState := b.state.Load()
	newState := b.state.Load()
	// it's necessary to merge states only from available upstreams
	availableUpstreams := b.availableUpstreams()

	newState.Status = b.processUpstreamStatuses()
	newState.Methods = processUpstreamMethods(availableUpstreams)
	newState.Blocks = processUpstreamBlocks(availableUpstreams)
	newState.LowerBounds = processLowerBounds(availableUpstreams)
	newState.ChainLabels = processLabels(availableUpstreams)
	newState.SubMethods = b.processSubMethods(availableUpstreams)

	eventWrappers := currentState.Compare(newState)
	if len(eventWrappers) > 0 {
		b.subStateManager.Publish(&ChainSupervisorStateWrapperEvent{eventWrappers})
	}

	b.state.Store(newState)
	b.calculateFinalizationLags()
}

func (b *BaseChainSupervisor) calculateFinalizationLags() {
	if b.tracker != nil {
		state := b.state.Load()

		b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
			finalizationBlock, ok := state.Blocks[protocol.FinalizedBlock]
			finalizationLag := uint64(0)
			if ok {
				finalizationLag = finalizationBlock.Height - val.BlockInfo.GetBlock(protocol.FinalizedBlock).Height
			}
			b.tracker.GetChainDimensions(b.chain, key).TrackFinalizationLag(finalizationLag)

			return true
		})
	}
}

func (b *BaseChainSupervisor) calculateHeadLags() {
	if b.tracker == nil {
		return
	}
	state := b.state.Load()

	b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		headLag := state.HeadData.Head.Height - val.HeadData.Height
		b.tracker.GetChainDimensions(b.chain, key).TrackHeadLag(headLag)
		return true
	})
}

func (b *BaseChainSupervisor) availableUpstreams() []*protocol.UpstreamState {
	states := make([]*protocol.UpstreamState, 0)

	b.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		if val.Status == protocol.Available {
			states = append(states, val)
		}
		return true
	})

	return states
}

func (b *BaseChainSupervisor) processSubMethods(availableUpstreams []*protocol.UpstreamState) mapset.Set[string] {
	for _, upState := range availableUpstreams {
		if upState.Caps.Contains(protocol.WsCap) {
			return b.subChainMethods.Clone()
		}
	}

	return mapset.NewThreadUnsafeSet[string]()
}

func (b *BaseChainSupervisor) processUpstreamStatuses() protocol.AvailabilityStatus {
	var status = protocol.Unavailable
	b.upstreamStates.Range(func(upId string, upState *protocol.UpstreamState) bool {
		if upState.Status < status {
			status = upState.Status
		}
		return true
	})

	return status
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
