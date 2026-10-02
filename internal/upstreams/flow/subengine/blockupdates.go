package subengine

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// historyRingSize bounds how deep a reorg we keep enough history to reconcile.
// Indexed by height % historyRingSize.
const historyRingSize = 18

// reorgClampedMetric counts reorgs whose orphaned suffix reaches deeper than the
// history ring, so the oldest orphans are never emitted as removed:true. A
// non-zero value means a reorg exceeded the reconciliation window and some
// removals were silently dropped.
var reorgClampedMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "reorg_clamped_total",
		Help:      "The total number of reorgs deeper than the history window whose oldest removals were dropped",
	},
	[]string{"chain"},
)

// backfillFailedMetric counts heads whose missing ancestors could not be fetched;
// such a head is announced as is, leaving the gap.
var backfillFailedMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "backfill_failed_total",
		Help:      "The total number of heads whose missing ancestors could not be fetched (announced with a gap)",
	},
	[]string{"chain"},
)

func init() {
	prometheus.MustRegister(reorgClampedMetric, backfillFailedMetric)
}

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
//   - advance alone does not backfill: heights the head jumped over (N -> N+2)
//     and the new chain below a reorged tip are never announced.
//     advanceWithAncestors fetches them first (see needsParent).
//   - Reorg reconciliation is bounded by the ring window: orphans deeper than
//     historyRingSize have already been evicted.
type blockTracker struct {
	ring    []ringEntry
	haveTip bool
	tipH    uint64
	chain   chains.Chain
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
		// Reorg reaches deeper than the ring: orphans below lo were already
		// evicted and are never emitted as removed. Surface this incompleteness.
		lo = t.tipH - historyRingSize + 1
		log.Warn().Msgf("subengine: reorg on %s deeper than history window (reorgFrom=%d clamped to %d, tip=%d); oldest removals dropped", t.chain, reorgFrom, lo, t.tipH)
		reorgClampedMetric.WithLabelValues(t.chain.String()).Inc()
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

// needsParent reports whether block does not link to what was announced, so its
// parent must be announced first: the head jumped over the parent's height, or
// the parent at an announced height has a different hash (a reorg below the tip
// whose new chain was never announced). A parent below the window or at a height
// never announced cannot be linked against and is left to advance.
func (t *blockTracker) needsParent(block protocol.Block) bool {
	if !t.haveTip || block.Height == 0 || len(block.ParentHash) == 0 {
		return false
	}
	ph := block.Height - 1
	if ph > t.tipH {
		return true
	}
	e := t.ring[ph%historyRingSize]
	return e.populated && e.height == ph && !e.hash.Equals(block.ParentHash)
}

// BlockResolver fetches a block header by hash.
type BlockResolver func(ctx context.Context, hash blockchain.HashId) (protocol.Block, error)

// maxBackfillBlocks bounds how many ancestors of one head are fetched; a deeper
// gap is left unfilled.
const maxBackfillBlocks = 128

// advanceWithAncestors is advance that first announces the ancestors block does
// not link to, fetched by parent hash and fed oldest-first, so their NEW/DROP
// updates come out of the regular advance logic. It errors when an ancestor
// cannot be fetched or the gap exceeds maxBackfillBlocks.
func (t *blockTracker) advanceWithAncestors(ctx context.Context, block protocol.Block, resolve BlockResolver) ([]BlockUpdate, error) {
	pending := []protocol.Block{block}
	for t.needsParent(pending[0]) {
		if len(pending) > maxBackfillBlocks {
			return nil, fmt.Errorf("block %d does not link within %d ancestors", block.Height, maxBackfillBlocks)
		}
		child := pending[0]
		parent, err := resolve(ctx, child.ParentHash)
		if err != nil {
			return nil, fmt.Errorf("fetch parent of block %d: %w", child.Height, err)
		}
		if parent.Height+1 != child.Height || !parent.Hash.Equals(child.ParentHash) {
			return nil, fmt.Errorf("parent of block %d resolved to %d %s", child.Height, parent.Height, parent.Hash.ToHexWithPrefix())
		}
		pending = append([]protocol.Block{parent}, pending...)
	}
	var updates []BlockUpdate
	for _, b := range pending {
		updates = append(updates, t.advance(b)...)
	}
	return updates, nil
}

// StreamBlockUpdates taps the chain head stream and pushes ordered BlockUpdates
// to out until srcCtx is cancelled or the head subscription closes. It owns its
// blockTracker and closes out on return, so the consumer exits deterministically
// (via its `if !ok` branch) even when the head subscription closes while srcCtx
// is still live. It backfills the heads' missing ancestors via resolve; a head
// whose ancestors cannot be fetched is logged, counted and announced as is.
func StreamBlockUpdates(srcCtx context.Context, chainSup upstreams.ChainSupervisor, out chan<- BlockUpdate, resolve BlockResolver) {
	defer close(out)

	sub := chainSup.SubscribeState(fmt.Sprintf("subengine_logs_%s_%s", chainSup.GetChain(), uuid.NewString()))
	defer sub.Unsubscribe()

	t := newBlockTracker()
	t.chain = chainSup.GetChain()
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
				updates, err := t.advanceWithAncestors(srcCtx, head.Head, resolve)
				if err != nil {
					if srcCtx.Err() != nil {
						return
					}
					log.Warn().Err(err).Msgf("subengine: cannot backfill the head of %s; announcing it with a gap", t.chain)
					backfillFailedMetric.WithLabelValues(t.chain.String()).Inc()
					updates = t.advance(head.Head)
				}
				for _, update := range updates {
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
