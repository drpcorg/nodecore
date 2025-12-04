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
		Help:      "Current availability status of the upstream: 0 = available, 1 = unavailable",
	},
	[]string{"chain", "upstream"},
)

func init() {
	prometheus.MustRegister(availabilityMetric)
}

type ChainSupervisor struct {
	ctx            context.Context
	Chain          chains.Chain
	fc             choice.ForkChoice
	state          *utils.Atomic[ChainSupervisorState]
	eventsChan     chan protocol.UpstreamEvent
	upstreamStates *utils.CMap[string, *protocol.UpstreamState]
	tracker        *dimensions.DimensionTracker
}

type ChainSupervisorState struct {
	Status   protocol.AvailabilityStatus
	HeadData ChainHeadData
	Methods  methods.Methods
	Blocks   map[protocol.BlockType]*protocol.BlockData
}

type ChainHeadData struct {
	Head       uint64
	UpstreamId string
}

func NewChainHeadData(head uint64, upstreamId string) ChainHeadData {
	return ChainHeadData{
		Head:       head,
		UpstreamId: upstreamId,
	}
}

func NewChainSupervisor(ctx context.Context, chain chains.Chain, fc choice.ForkChoice, tracker *dimensions.DimensionTracker) *ChainSupervisor {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(
		ChainSupervisorState{
			Status:   protocol.Available,
			Blocks:   make(map[protocol.BlockType]*protocol.BlockData),
			HeadData: NewChainHeadData(0, ""),
		},
	)

	return &ChainSupervisor{
		ctx:            ctx,
		tracker:        tracker,
		Chain:          chain,
		fc:             fc,
		eventsChan:     make(chan protocol.UpstreamEvent, 100),
		upstreamStates: utils.NewCMap[string, *protocol.UpstreamState](),
		state:          utils.NewAtomic[ChainSupervisorState](),
	}
}

func (c *ChainSupervisor) Start() {
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

func (c *ChainSupervisor) GetChainState() ChainSupervisorState {
	return c.state.Load()
}

func (c *ChainSupervisor) GetMethod(methodName string) *specs.Method {
	return c.GetChainState().Methods.GetMethod(methodName)
}

func (c *ChainSupervisor) GetMethods() []string {
	if c.GetChainState().Methods == nil {
		return nil
	}
	return c.GetChainState().Methods.GetSupportedMethods().ToSlice()
}

func (c *ChainSupervisor) Publish(event protocol.UpstreamEvent) {
	c.eventsChan <- event
}

func (c *ChainSupervisor) GetUpstreamState(upstreamId string) *protocol.UpstreamState {
	if s, ok := c.upstreamStates.Load(upstreamId); ok {
		return s
	}
	return nil
}

func (c *ChainSupervisor) GetSortedUpstreamIds(
	filterFunc func(id string, state *protocol.UpstreamState) bool,
	sortFunc func(entry1, entry2 lo.Tuple2[string, *protocol.UpstreamState]) int,
) []string {
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

func (c *ChainSupervisor) GetUpstreamIds() []string {
	ids := make([]string, 0)
	c.upstreamStates.Range(func(upId string, _ *protocol.UpstreamState) bool {
		ids = append(ids, upId)
		return true
	})
	slices.Sort(ids)
	return ids
}

func (c *ChainSupervisor) processEvents() {
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

func (c *ChainSupervisor) updateState(upstreamId string, upstreamState *protocol.StateUpstreamEvent) {
	state := c.state.Load()
	// it's necessary to merge states only from available upstreams
	availableUpstreams := c.availableUpstreams()

	if upstreamState != nil && upstreamState.State.HeadData != nil {
		updated, headHeight := c.fc.Choose(upstreamId, upstreamState)
		if updated {
			state.HeadData = NewChainHeadData(headHeight, upstreamId)
		}
	} else if upstreamState != nil {
		state.HeadData = NewChainHeadData(0, upstreamId)
	}
	state.Status = c.processUpstreamStatuses()
	state.Methods = c.processUpstreamMethods(availableUpstreams)
	state.Blocks = c.processUpstreamBlocks(availableUpstreams)

	c.state.Store(state)

	c.calculateLags()
}

func (c *ChainSupervisor) calculateLags() {
	if c.tracker != nil {
		state := c.state.Load()

		c.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
			headLag := state.HeadData.Head - val.HeadData.Height
			finalizationBlock, ok := state.Blocks[protocol.FinalizedBlock]
			finalizationLag := uint64(0)
			if ok {
				finalizationLag = finalizationBlock.Height - val.BlockInfo.GetBlock(protocol.FinalizedBlock).Height
			}
			c.tracker.TrackLags(c.Chain, key, headLag, finalizationLag)

			return true
		})
	}
}

func (c *ChainSupervisor) availableUpstreams() []*protocol.UpstreamState {
	states := make([]*protocol.UpstreamState, 0)

	c.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
		if val.Status == protocol.Available {
			states = append(states, val)
		}
		return true
	})

	return states
}

func (c *ChainSupervisor) processUpstreamMethods(availableStates []*protocol.UpstreamState) methods.Methods {
	delegates := lo.Map(availableStates, func(item *protocol.UpstreamState, index int) methods.Methods {
		return item.UpstreamMethods
	})

	return methods.NewChainMethods(delegates)
}

func (c *ChainSupervisor) processUpstreamStatuses() protocol.AvailabilityStatus {
	var status = protocol.Unavailable
	c.upstreamStates.Range(func(upId string, upState *protocol.UpstreamState) bool {
		if upState.Status < status {
			status = upState.Status
		}
		return true
	})

	return status
}

func (c *ChainSupervisor) processUpstreamBlocks(availableStates []*protocol.UpstreamState) map[protocol.BlockType]*protocol.BlockData {
	blocks := map[protocol.BlockType]*protocol.BlockData{}

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

func compareBlocks(blockType protocol.BlockType, currentBlock, newBlock *protocol.BlockData) *protocol.BlockData {
	switch blockType {
	case protocol.FinalizedBlock:
		if newBlock.Height > currentBlock.Height {
			return newBlock
		}
	}
	return currentBlock
}

func (c *ChainSupervisor) monitor() {
	state := c.state.Load()

	var height string
	if state.HeadData.Head > 0 {
		height = fmt.Sprintf("%d", state.HeadData.Head)
	} else {
		height = "?"
	}

	statuses := make(map[protocol.AvailabilityStatus]int)
	c.upstreamStates.Range(func(key string, upState *protocol.UpstreamState) bool {
		statuses[upState.Status]++

		return true
	})

	upstreamStatuses, weakUpstreams := c.getStatuses()

	log.Info().Msgf("State of %s: height=%s, statuses=[%s], weak=[%s]", strings.ToUpper(c.Chain.String()), height, upstreamStatuses, weakUpstreams)
}

func (c *ChainSupervisor) getStatuses() (string, string) {
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
