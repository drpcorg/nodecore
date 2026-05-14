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

// Choose recomputes the merged head from currently-live upstream heads.
//
// Available events with a non-empty head update the tracked head for that upstream.
// Any other status removes the upstream from the live set.
// In both cases the new max is recomputed from scratch — so the merged head can move
// down (reorg, rollback, primary peer switch, ungraceful resubscribe) just as freely
// as it can move up.
//
// Empty-by-height Available events are ignored so that status-only or unsynced events
// can't erase the last known head for an upstream.
//
// Not safe for concurrent use; expects serialized calls from the supervisor event loop.
func (h *HeightForkChoice) Choose(upstreamId string, event *protocol.HeadUpstreamEvent) (bool, protocol.Block) {
	if event.Status == protocol.Available {
		if event.Head.IsEmptyByHeight() {
			return false, h.max
		}
		h.heads[upstreamId] = event.Head
	} else {
		if _, ok := h.heads[upstreamId]; !ok {
			return false, h.max
		}
		delete(h.heads, upstreamId)
	}

	newMax := h.maxHeight()
	if newMax.CompareWithHeight(h.max) == 0 {
		return false, h.max
	}
	h.max = newMax
	return true, h.max
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
