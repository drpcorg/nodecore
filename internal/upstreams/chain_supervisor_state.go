package upstreams

import (
	"maps"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/samber/lo"
)

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

func (c ChainHeadData) IsEmpty() bool {
	return c.Head.IsEmptyByHeight()
}

type AggregatedLabels struct {
	Amount int
	Labels map[string]string
}

func NewAggregatedLabels(amount int, labels map[string]string) AggregatedLabels {
	return AggregatedLabels{
		Amount: amount,
		Labels: labels,
	}
}

func (a AggregatedLabels) Equals(other AggregatedLabels) bool {
	return a.Amount == other.Amount && maps.Equal(a.Labels, other.Labels)
}

func CompareAggregatedLabels(a, b []AggregatedLabels) bool {
	if len(a) != len(b) {
		return false
	}
	for _, labelA := range a {
		_, ok := lo.Find(b, func(item AggregatedLabels) bool {
			return item.Equals(labelA)
		})
		if !ok {
			return false
		}
	}
	return true
}

type ChainSupervisorState struct {
	Status      protocol.AvailabilityStatus
	HeadData    ChainHeadData
	Methods     methods.Methods
	Blocks      map[protocol.BlockType]protocol.Block
	LowerBounds map[protocol.LowerBoundType]protocol.LowerBoundData
	ChainLabels []AggregatedLabels
	SubMethods  mapset.Set[string]
}

func (c ChainSupervisorState) Compare(new ChainSupervisorState) []ChainSupervisorStateWrapper {
	wrappers := make([]ChainSupervisorStateWrapper, 0)

	if c.Status != new.Status {
		wrappers = append(wrappers, NewStatusWrapper(new.Status))
	}

	if !c.Methods.GetSupportedMethods().Equal(new.Methods.GetSupportedMethods()) {
		wrappers = append(wrappers, NewMethodsWrapper(new.Methods.GetSupportedMethods().ToSlice()))
	}

	compareBlocksFunc := func(b1, b2 protocol.Block) bool {
		return b1.Equals(b2)
	}
	if !maps.EqualFunc(c.Blocks, new.Blocks, compareBlocksFunc) {
		wrappers = append(wrappers, NewBlocksWrapper(new.Blocks))
	}

	if !maps.Equal(c.LowerBounds, new.LowerBounds) {
		wrappers = append(wrappers, NewLowerBoundsWrapper(lo.Values(new.LowerBounds)))
	}

	if !CompareAggregatedLabels(c.ChainLabels, new.ChainLabels) {
		wrappers = append(wrappers, NewLabelsWrapper(new.ChainLabels))
	}

	if !c.SubMethods.Equal(new.SubMethods) {
		wrappers = append(wrappers, NewSubMethodsWrapper(new.SubMethods.ToSlice()))
	}

	return wrappers
}

func processLabels(availableUpstreams []*protocol.UpstreamState) []AggregatedLabels {
	allLabels := make([]AggregatedLabels, 0)

	for _, upState := range availableUpstreams {
		if upState.Labels == nil {
			continue
		}
		upLabels := upState.Labels.GetAllLabels()
		if len(upLabels) == 0 {
			continue
		}

		_, idx, ok := lo.FindIndexOf(allLabels, func(item AggregatedLabels) bool {
			return maps.Equal(upLabels, item.Labels)
		})
		if !ok {
			allLabels = append(allLabels, NewAggregatedLabels(1, upLabels))
		} else {
			allLabels[idx].Amount++
		}
	}
	return allLabels
}

func processUpstreamMethods(availableStates []*protocol.UpstreamState) methods.Methods {
	delegates := lo.Map(availableStates, func(item *protocol.UpstreamState, index int) methods.Methods {
		return item.UpstreamMethods
	})

	return methods.NewChainMethods(delegates)
}

func processLowerBounds(availableStates []*protocol.UpstreamState) map[protocol.LowerBoundType]protocol.LowerBoundData {
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

func processUpstreamBlocks(availableStates []*protocol.UpstreamState) map[protocol.BlockType]protocol.Block {
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
