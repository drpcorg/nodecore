package upstreams

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	choice "github.com/drpcorg/dsheltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"slices"
	"strings"
	"time"
)

type ChainSupervisor struct {
	ctx            context.Context
	Chain          chains.Chain
	fc             choice.ForkChoice
	state          *utils.Atomic[ChainSupervisorState]
	eventsChan     chan protocol.UpstreamEvent
	upstreamStates *utils.CMap[string, protocol.UpstreamState]
	tracker        *dimensions.DimensionTracker
}

type ChainSupervisorState struct {
	Status  protocol.AvailabilityStatus
	Head    uint64
	Methods methods.Methods
	Blocks  map[protocol.BlockType]*protocol.BlockData
}

func NewChainSupervisor(ctx context.Context, chain chains.Chain, fc choice.ForkChoice, tracker *dimensions.DimensionTracker) *ChainSupervisor {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(ChainSupervisorState{Status: protocol.Available, Blocks: make(map[protocol.BlockType]*protocol.BlockData)})

	return &ChainSupervisor{
		ctx:            ctx,
		tracker:        tracker,
		Chain:          chain,
		fc:             fc,
		eventsChan:     make(chan protocol.UpstreamEvent, 100),
		upstreamStates: utils.NewCMap[string, protocol.UpstreamState](),
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
				state := c.state.Load()
				c.upstreamStates.Store(event.Id, event.State)

				// it's necessary to merge states only from available upstreams
				availableUpstreams := c.availableUpstreams()

				if event.State.HeadData != nil {
					updated, headHeight := c.fc.Choose(event)
					if updated {
						state.Head = headHeight
					}
				}
				state.Status = c.processUpstreamStatuses()
				state.Methods = c.processUpstreamMethods(availableUpstreams)
				state.Blocks = c.processUpstreamBlocks(availableUpstreams)

				c.state.Store(state)

				c.calculateLags()
			}
		}
	}
}

func (c *ChainSupervisor) calculateLags() {
	if c.tracker != nil {
		state := c.state.Load()

		c.upstreamStates.Range(func(key string, val *protocol.UpstreamState) bool {
			headLag := state.Head - val.HeadData.Height
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
	var status protocol.AvailabilityStatus = protocol.UnknownStatus
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
	if state.Head > 0 {
		height = fmt.Sprintf("%d", state.Head)
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
