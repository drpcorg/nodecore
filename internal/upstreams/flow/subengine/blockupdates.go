package subengine

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/google/uuid"
)

// historyRingSize bounds how deep a reorg we keep enough history to reconcile.
// Indexed by height % historyRingSize.
const historyRingSize = 18

// UpdateKind classifies a BlockUpdate.
type UpdateKind int

const (
	// BlockNew marks a block that just became canonical; its logs are emitted
	// with removed:false.
	BlockNew UpdateKind = iota
	// BlockDrop marks a previously-canonical block that was orphaned by a reorg;
	// its cached logs are re-emitted with removed:true.
	BlockDrop
)

// BlockUpdate is a single canonical-chain transition derived from the merged
// head stream. No upstream id is carried: the logs source picks an upstream by
// the block's height, not by the head producer.
type BlockUpdate struct {
	Block protocol.Block
	Kind  UpdateKind
}

// ringEntry remembers the (height, hash) the tracker last considered canonical at
// a height. Reorg detection compares this stored hash against the arriving head
// (its own hash for a same/lower height, its parent hash for a direct successor),
// so the parent hash of the stored block is not needed.
type ringEntry struct {
	populated bool
	height    uint64
	hash      blockchain.HashId
}

// blockTracker turns the merged head stream into ordered NEW/DROP updates. It
// owns a bounded history ring keyed by height and is single-goroutine (no
// locking). Classification is by HASH per height, not by height alone, so a
// benign rollback (a faster upstream dropping out, leaving a lower head from a
// slower upstream on the same chain) is a no-op, while a head that disagrees on
// the hash at a height we already announced is a reorg.
//
// The merged head fork-choice (fork_choice/height_fc.go) republishes a head only
// when the max height CHANGES, which still constrains what this tracker can see:
//   - A same-height 1-block reorg with no height movement is invisible (it
//     self-heals on the next height change via the parent/hash mismatch).
//   - Forward gaps (height jumps N -> N+2) are not backfilled.
//   - Reorg reconciliation is bounded by the ring window: orphans deeper than
//     historyRingSize have already been evicted, and the reorged-in block at the
//     same height as a dropped tip is not backfilled.
type blockTracker struct {
	ring    []ringEntry
	haveTip bool
	tipH    uint64
}

func newBlockTracker() *blockTracker {
	return &blockTracker{ring: make([]ringEntry, historyRingSize)}
}

// advance applies a merged head to the tracker and returns the resulting block
// updates: zero or more BlockDrop (a reorged-out suffix, top-down) followed by an
// optional BlockNew. It is pure with respect to channels/goroutines, so it can be
// table-tested directly.
func (t *blockTracker) advance(block protocol.Block) []BlockUpdate {
	if len(block.Hash) == 0 {
		return nil // cannot key getLogs or the ring without a hash
	}
	h := block.Height

	if !t.haveTip {
		t.put(block)
		t.haveTip = true
		t.tipH = h
		return []BlockUpdate{{Block: block, Kind: BlockNew}}
	}

	// A block we already announced at this exact height+hash: a benign re-report
	// (e.g. a slower upstream becoming the head after a faster one drops) or a
	// plain duplicate. Nothing to do.
	if e := t.ring[h%historyRingSize]; e.populated && e.height == h && e.hash.Equals(block.Hash) {
		return nil
	}

	if reorgFrom, ok := t.reorgPoint(block); ok {
		updates := t.dropFrom(reorgFrom)
		t.put(block)
		t.tipH = h
		return append(updates, BlockUpdate{Block: block, Kind: BlockNew})
	}

	if h > t.tipH {
		// Clean extension, or a forward gap we cannot reconcile - just announce it.
		t.put(block)
		t.tipH = h
		return []BlockUpdate{{Block: block, Kind: BlockNew}}
	}

	// h <= tipH with no block announced at this height: a lower head we can't
	// reason about (e.g. below a gap). Don't drop, don't backfill.
	return nil
}

// reorgPoint reports the lowest announced height orphaned by block, and whether
// block reorgs anything we have already announced. An exact height+hash match is
// handled as a duplicate by the caller before this is reached.
func (t *blockTracker) reorgPoint(block protocol.Block) (uint64, bool) {
	h := block.Height
	if h > t.tipH {
		// Forward: only a direct successor whose parent disagrees with our tip
		// reveals a (tip) reorg. Forward gaps can't be reasoned about.
		if h == t.tipH+1 && len(block.ParentHash) > 0 {
			if e := t.ring[t.tipH%historyRingSize]; e.populated && e.height == t.tipH && !e.hash.Equals(block.ParentHash) {
				return t.tipH, true
			}
		}
		return 0, false
	}
	// h <= tipH with a different hash at a height we announced: the chain reorged
	// at h, orphaning everything we announced from h up to the tip.
	if e := t.ring[h%historyRingSize]; e.populated && e.height == h {
		return h, true
	}
	return 0, false
}

// dropFrom emits BlockDrop for every announced block in [reorgFrom..tipH],
// top-down, and clears them. It is bounded to the last historyRingSize heights
// (older entries have already been evicted from the ring).
func (t *blockTracker) dropFrom(reorgFrom uint64) []BlockUpdate {
	lo := reorgFrom
	if t.tipH >= historyRingSize && lo < t.tipH-historyRingSize+1 {
		lo = t.tipH - historyRingSize + 1
	}
	var drops []BlockUpdate
	for hh := t.tipH; hh >= lo; hh-- {
		if e := t.ring[hh%historyRingSize]; e.populated && e.height == hh {
			drops = append(drops, BlockUpdate{Block: protocol.Block{Height: e.height, Hash: e.hash}, Kind: BlockDrop})
			t.ring[hh%historyRingSize] = ringEntry{}
		}
		if hh == 0 {
			break // avoid uint64 underflow
		}
	}
	return drops
}

func (t *blockTracker) put(block protocol.Block) {
	t.ring[block.Height%historyRingSize] = ringEntry{
		populated: true,
		height:    block.Height,
		hash:      block.Hash,
	}
}

// StreamBlockUpdates taps the chain head stream and pushes ordered BlockUpdates
// to out until srcCtx is cancelled or the head subscription closes. It owns its
// blockTracker; the caller owns out (it is not closed here).
func StreamBlockUpdates(srcCtx context.Context, chainSup upstreams.ChainSupervisor, out chan<- BlockUpdate) {
	sub := chainSup.SubscribeState(fmt.Sprintf("subengine_logs_%s_%s", chainSup.GetChain(), uuid.NewString()))
	defer sub.Unsubscribe()

	t := newBlockTracker()
	for {
		select {
		case <-srcCtx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			for _, wrapper := range event.Wrappers {
				head, ok := wrapper.(*upstreams.HeadWrapper)
				if !ok {
					continue
				}
				for _, update := range t.advance(head.Head) {
					select {
					case out <- update:
					case <-srcCtx.Done():
						return
					}
				}
			}
		}
	}
}
