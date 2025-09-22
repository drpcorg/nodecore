package fork_choice

import (
	"github.com/drpcorg/nodecore/internal/protocol"
)

type HeightForkChoice struct {
	heights map[string]uint64
	max     uint64
}

func NewHeightForkChoice() *HeightForkChoice {
	return &HeightForkChoice{
		heights: make(map[string]uint64),
	}
}

var _ ForkChoice = (*HeightForkChoice)(nil)

func (h *HeightForkChoice) Choose(event protocol.UpstreamEvent) (bool, uint64) {
	if event.State.Status == protocol.Available {
		h.heights[event.Id] = event.State.HeadData.Height

		currentMaxHeight := h.maxHeight()
		if currentMaxHeight > h.max {
			h.max = currentMaxHeight
			return true, h.max
		}
		return false, h.max
	} else {
		delete(h.heights, event.Id)

		currentMaxHeight := h.maxHeight()
		if currentMaxHeight == h.max {
			return false, h.max
		} else {
			h.max = currentMaxHeight
			return true, h.max
		}
	}
}

func (h *HeightForkChoice) maxHeight() uint64 {
	var currentMaxHeight uint64
	for _, height := range h.heights {
		if height > currentMaxHeight {
			currentMaxHeight = height
		}
	}
	return currentMaxHeight
}
