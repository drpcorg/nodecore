package fork_choice

import (
	"github.com/drpcorg/nodecore/internal/protocol"
)

type HeightForkChoice struct {
	heads map[string]protocol.Block
	max   protocol.Block
}

func NewHeightForkChoice() *HeightForkChoice {
	return &HeightForkChoice{
		heads: make(map[string]protocol.Block),
	}
}

var _ ForkChoice = (*HeightForkChoice)(nil)

func (h *HeightForkChoice) Choose(upstreamId string, event *protocol.StateUpstreamEvent) (bool, protocol.Block) {
	if event.State.Status == protocol.Available {
		h.heads[upstreamId] = event.State.HeadData

		currentMaxHeight := h.maxHeight()
		if currentMaxHeight.CompareWithHeight(h.max) == 1 {
			h.max = currentMaxHeight
			return true, h.max
		}
		return false, h.max
	}
	if _, ok := h.heads[upstreamId]; ok {
		delete(h.heads, upstreamId)

		currentMaxHeight := h.maxHeight()
		if currentMaxHeight.CompareWithHeight(h.max) == 0 {
			return false, h.max
		}

		h.max = currentMaxHeight
		return true, h.max
	}
	return false, h.max
}

func (h *HeightForkChoice) maxHeight() protocol.Block {
	var currentMaxHeight protocol.Block
	for _, head := range h.heads {
		if head.CompareWithHeight(currentMaxHeight) == 1 {
			currentMaxHeight = head
		}
	}
	return currentMaxHeight
}
