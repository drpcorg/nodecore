package upstreams

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

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
	ctx            context.Context
	Chain          chains.Chain
	fc             choice.ForkChoice
	state          *utils.Atomic[ChainSupervisorState]
	eventsChan     chan protocol.UpstreamEvent
	upstreamStates *utils.CMap[string, *protocol.UpstreamState]
	tracker        dimensions.DimensionTracker
}

type ChainSupervisorState struct {
	Status      protocol.AvailabilityStatus
	HeadData    ChainHeadData
	Methods     methods.Methods
	Blocks      map[protocol.BlockType]protocol.Block
	LowerBounds map[protocol.LowerBoundType]protocol.LowerBoundData
}

type ChainHeadData struct {
	Head       protocol.Block
	UpstreamId string
}

func NewChainHeadData(head protocol.Block, upstreamId string) ChainHeadData {
	return ChainHeadData{
		Head:       head,
		UpstreamId: upstreamId,
	}
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
		},
	)

	return &BaseChainSupervisor{
		ctx:            ctx,
		tracker:        tracker,
		Chain:          chain,
		fc:             fc,
		eventsChan:     make(chan protocol.UpstreamEvent, 100),
		upstreamStates: utils.NewCMap[string, *protocol.UpstreamState](),
		state:          state,
	}
}

func (c *BaseChainSupervisor) GetChain() chains.Chain {
	return c.Chain
}

func (c *BaseChainSupervisor) Start() {
	go c.processEvents()

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}

			c.monitor()
		}
	}()
}

func (c *BaseChainSupervisor) GetChainState() ChainSupervisorState {
	return c.state.Load()
}

func (c *BaseChainSupervisor) GetMethod(methodName string) *specs.Method {
	return c.GetChainState().Methods.GetMethod(methodName)
}

func (c *BaseChainSupervisor) GetMethods() []string {
	if c.GetChainState().Methods == nil {
		return nil
	}
	return c.GetChainState().Methods.GetSupportedMethods().ToSlice()
}

func (c *BaseChainSupervisor) PublishUpstreamEvent(event protocol.UpstreamEvent) {
	c.eventsChan <- event
}

func (c *BaseChainSupervisor) GetUpstreamState(upstreamId string) *protocol.UpstreamState {
	if s, ok := c.upstreamStates.Load(upstreamId); ok {
		return s
	}
	return nil
}

func (c *BaseChainSupervisor) GetSortedUpstreamIds(filterFunc FilterUpstream, sortFunc SortUpstream) []string {
	entries := make([]lo.Tuple2[string, *protocol.UpstreamState], 0)
	c.upstreamStates.Range(func(upId string, state *protocol.UpstreamState) bool {
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

func (c *BaseChainSupervisor) GetUpstreamIds() []string {
	ids := make([]string, 0)
	c.upstreamStates.Range(func(upId string, _ *protocol.UpstreamState) bool {
		ids = append(ids, upId)
		return true
	})
	slices.Sort(ids)
	return ids
}

func (c *BaseChainSupervisor) processEvents() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case event, ok := <-c.eventsChan:
			if ok {
				switch eventType := event.EventType.(type) {
				case *protocol.RemoveUpstreamEvent:
					c.upstreamStates.Delete(event.Id)
					c.updateState(event.Id, nil)
				case *protocol.StateUpstreamEvent:
					availabilityMetric.WithLabelValues(c.Chain.String(), event.Id).Set(float64(eventType.State.Status))
					c.upstreamStates.Store(event.Id, eventType.State)
					c.updateState(event.Id, eventType)
				}
			}
		}
	}
}

func (c *BaseChainSupervisor) updateState(upstreamId string, upstreamState *protocol.StateUpstreamEvent) {
	state := c.state.Load()
	// it's necessary to merge states only from available upstreams
	availableUpstreams := c.availableUpstreams()

	if upstreamState != nil && !upstreamState.State.HeadData.IsEmptyByHeight() {
		updated, head := c.fc.Choose(upstreamId, upstreamState)
		if updated {
			state.HeadData = NewChainHeadData(head, upstreamId)
		}
	} else if upstreamState != nil {
		state.HeadData = NewChainHeadData(protocol.ZeroBlock{}, upstreamId)
	}
	state.Status = c.processUpstreamStatuses()
	state.Methods = c.processUpstreamMethods(availableUpstreams)
	state.Blocks = c.processUpstreamBlocks(availableUpstreams)
	state.LowerBounds = c.processLowerBounds(availableUpstreams)

	c.state.Store(state)

	c.calculateLags()
}

func (c *BaseChainSupervisor) calculateLags() {
	if c.tracker != nil {
		state := c.state.Load()

		c.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
			headLag := state.HeadData.Head.Height - val.HeadData.Height
			finalizationBlock, ok := state.Blocks[protocol.FinalizedBlock]
			finalizationLag := uint64(0)
			if ok {
				finalizationLag = finalizationBlock.Height - val.BlockInfo.GetBlock(protocol.FinalizedBlock).Height
			}
			c.tracker.GetChainDimensions(c.Chain, key).TrackLags(headLag, finalizationLag)

			return true
		})
	}
}

func (c *BaseChainSupervisor) availableUpstreams() []*protocol.UpstreamState {
	states := make([]*protocol.UpstreamState, 0)

	c.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		if val.Status == protocol.Available {
			states = append(states, val)
		}
		return true
	})

	return states
}

func (c *BaseChainSupervisor) processUpstreamMethods(availableStates []*protocol.UpstreamState) methods.Methods {
	delegates := lo.Map(availableStates, func(item *protocol.UpstreamState, index int) methods.Methods {
		return item.UpstreamMethods
	})

	return methods.NewChainMethods(delegates)
}

func (c *BaseChainSupervisor) processLowerBounds(availableStates []*protocol.UpstreamState) map[protocol.LowerBoundType]protocol.LowerBoundData {
	bounds := make(map[protocol.LowerBoundType]protocol.LowerBoundData)

	for _, upsState := range availableStates {
		if upsState.LowerBoundsInfo == nil {
			continue
		}
		upBounds := upsState.LowerBoundsInfo.GetAllBounds()
		for _, bound := range upBounds {
			currentBound, ok := bounds[bound.Type]
			if !ok || bound.Bound < currentBound.Bound {
				bounds[bound.Type] = bound
			}
		}
	}

	return bounds
}

func (c *BaseChainSupervisor) processUpstreamStatuses() protocol.AvailabilityStatus {
	var status = protocol.Unavailable
	c.upstreamStates.Range(func(upId string, upState *protocol.UpstreamState) bool {
		if upState.Status < status {
			status = upState.Status
		}
		return true
	})

	return status
}

func (c *BaseChainSupervisor) processUpstreamBlocks(availableStates []*protocol.UpstreamState) map[protocol.BlockType]protocol.Block {
	blocks := make(map[protocol.BlockType]protocol.Block, len(availableStates))

	for _, upState := range availableStates {
		if upState.BlockInfo != nil {
			upBlocks := upState.BlockInfo.GetBlocks()

			for blockType, blockData := range upBlocks {
				currentBlockData, ok := blocks[blockType]
				if !ok {
					blocks[blockType] = blockData
				} else {
					blocks[blockType] = compareBlocks(blockType, currentBlockData, blockData)
				}
			}
		}
	}

	return blocks
}

func compareBlocks(blockType protocol.BlockType, currentBlock, newBlock protocol.Block) protocol.Block {
	switch blockType {
	case protocol.FinalizedBlock:
		if newBlock.Height > currentBlock.Height {
			return newBlock
		}
	}
	return currentBlock
}

func (c *BaseChainSupervisor) monitor() {
	state := c.state.Load()

	var height string
	if state.HeadData.Head.Height > 0 {
		height = fmt.Sprintf("%d", state.HeadData.Head.Height)
	} else {
		height = "?"
	}

	statuses := make(map[protocol.AvailabilityStatus]int)
	c.upstreamStates.Range(func(key string, upState *protocol.UpstreamState) bool {
		statuses[upState.Status]++

		return true
	})
	boundsSlice := lo.MapToSlice(state.LowerBounds, func(key protocol.LowerBoundType, val protocol.LowerBoundData) string {
		return fmt.Sprintf("%s=%d", key, val.Bound)
	})
	bounds := strings.Join(boundsSlice, ", ")

	upstreamStatuses, weakUpstreams := c.getStatuses()

	log.Info().Msgf(
		"State of %s: height=%s, statuses=[%s], bounds=[%s], weak=[%s]",
		strings.ToUpper(c.Chain.String()),
		height,
		upstreamStatuses,
		bounds,
		weakUpstreams,
	)
}

func (c *BaseChainSupervisor) getStatuses() (string, string) {
	statuses := make(map[protocol.AvailabilityStatus]int)
	weakUpstreams := make([]string, 0)
	c.upstreamStates.Range(func(upId string, upState *protocol.UpstreamState) bool {
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
