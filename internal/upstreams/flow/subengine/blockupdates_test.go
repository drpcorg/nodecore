package subengine

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blk(height uint64, hash, parent string) protocol.Block {
	return protocol.Block{
		Height:     height,
		Hash:       blockchain.NewHashIdFromString(hash),
		ParentHash: blockchain.NewHashIdFromString(parent),
	}
}

type wantUpdate struct {
	height uint64
	hash   string
	kind   UpdateKind
}

func assertUpdates(t *testing.T, got []BlockUpdate, want []wantUpdate) {
	t.Helper()
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, w.height, got[i].Block.Height, "update %d height", i)
		assert.Equal(t, blockchain.NewHashIdFromString(w.hash), got[i].Block.Hash, "update %d hash", i)
		assert.Equal(t, w.kind, got[i].Kind, "update %d kind", i)
	}
}

func TestBlockTrackerLinearExtension(t *testing.T) {
	tr := newBlockTracker()
	assertUpdates(t, tr.advance(blk(1, "aa", "00")), []wantUpdate{{1, "aa", BlockNew}})
	assertUpdates(t, tr.advance(blk(2, "bb", "aa")), []wantUpdate{{2, "bb", BlockNew}})
	assertUpdates(t, tr.advance(blk(3, "cc", "bb")), []wantUpdate{{3, "cc", BlockNew}})
}

func TestBlockTrackerForwardGapEmitsOnlyNew(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	// Height jumps 1 -> 3: the intermediate block was never delivered, so no DROP
	// and no backfill - just NEW for height 3.
	assertUpdates(t, tr.advance(blk(3, "cc", "bb")), []wantUpdate{{3, "cc", BlockNew}})
}

func TestBlockTrackerDepth1Reorg(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa")) // canonical at height 2 is "bb"
	// New head at height 3 builds on "b2" (a different height-2 block), so the
	// "bb" we had at height 2 is orphaned: DROP("bb") then NEW("cc").
	got := tr.advance(blk(3, "cc", "b2"))
	assertUpdates(t, got, []wantUpdate{{2, "bb", BlockDrop}, {3, "cc", BlockNew}})
}

// A faster upstream drops out and the merged head falls back to a lower head from
// a slower upstream on the SAME chain (same hashes). Nothing is dropped.
func TestBlockTrackerBenignRollbackNoOp(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(3, "cc", "bb")) // tip = 3 via the fast upstream

	// Fast upstream gone; head rolls back to the slower upstream's head, still on
	// the same chain - each height keeps its hash, so no DROP, no NEW.
	assert.Empty(t, tr.advance(blk(2, "bb", "aa")), "benign rollback to a known block")
	assert.Empty(t, tr.advance(blk(1, "aa", "00")), "benign rollback further down")

	// The slow upstream catches back up: known blocks are no-ops, new ones NEW.
	assert.Empty(t, tr.advance(blk(2, "bb", "aa")))
	assert.Empty(t, tr.advance(blk(3, "cc", "bb")))
	assertUpdates(t, tr.advance(blk(4, "dd", "cc")), []wantUpdate{{4, "dd", BlockNew}})
}

// A reorg that surfaces BELOW the current tip (a head at an already-announced
// height with a different hash) drops the orphaned suffix [h..tip] top-down and
// announces the new block.
func TestBlockTrackerReorgBelowTip(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(3, "cc", "bb")) // tip = 3

	// Height 2 reappears with a different hash -> reorg at 2: drop cc@3 then bb@2,
	// then announce the new b2@2.
	got := tr.advance(blk(2, "b2", "aa"))
	assertUpdates(t, got, []wantUpdate{{3, "cc", BlockDrop}, {2, "bb", BlockDrop}, {2, "b2", BlockNew}})
}

func TestBlockTrackerDuplicateAndStaleAreNoOps(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	assert.Empty(t, tr.advance(blk(2, "bb", "aa")), "exact duplicate -> no update")
	assert.Empty(t, tr.advance(blk(1, "aa", "00")), "stale lower height -> no update")
}

func TestBlockTrackerSameHeightSwapIsDefensivelyHandled(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	// A same-height head with a different hash (the fork choice normally suppresses
	// this, but if it ever surfaces) drops the old tip and emits the new one.
	got := tr.advance(blk(2, "b2", "aa"))
	assertUpdates(t, got, []wantUpdate{{2, "bb", BlockDrop}, {2, "b2", BlockNew}})
}

func TestBlockTrackerEmptyHashSkipped(t *testing.T) {
	tr := newBlockTracker()
	assert.Empty(t, tr.advance(protocol.Block{Height: 5}), "no hash -> no update")
}

func TestBlockTrackerRingWraparoundNoSpuriousDrop(t *testing.T) {
	tr := newBlockTracker()
	// Advance well past the ring size so old entries are evicted/overwritten.
	parent := "00"
	for h := uint64(1); h <= historyRingSize+5; h++ {
		cur := hexFor(h)
		assertUpdates(t, tr.advance(blk(h, cur, parent)), []wantUpdate{{h, cur, BlockNew}})
		parent = cur
	}
	// A reorg deeper than the ring can hold: the displaced predecessor at h-1 is
	// still in the ring (depth-1), so exactly one DROP + one NEW, never more.
	top := historyRingSize + 5
	got := tr.advance(blk(uint64(top+1), "ff", "deadbeef"))
	require.Len(t, got, 2)
	assert.Equal(t, BlockDrop, got[0].Kind)
	assert.Equal(t, BlockNew, got[1].Kind)
}

// --- additional coverage -------------------------------------------------

func noUpdate(t *testing.T, tr *blockTracker, b protocol.Block, msg string) {
	t.Helper()
	assert.Empty(t, tr.advance(b), msg)
}

// The very first head is always announced as NEW regardless of its parent.
func TestBlockTrackerFirstBlockEmitsNew(t *testing.T) {
	tr := newBlockTracker()
	assertUpdates(t, tr.advance(blk(7, "aa", "00")), []wantUpdate{{7, "aa", BlockNew}})
}

// A direct successor with no parent hash cannot be classified as a reorg, so it
// is announced as a clean extension (no false DROP).
func TestBlockTrackerCleanExtensionEmptyParentNoReorg(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	got := tr.advance(protocol.Block{Height: 2, Hash: blockchain.NewHashIdFromString("bb")}) // empty parent
	assertUpdates(t, got, []wantUpdate{{2, "bb", BlockNew}})
}

// Re-announcing the exact tip is a no-op.
func TestBlockTrackerExactTipReannounceNoOp(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	noUpdate(t, tr, blk(2, "bb", "aa"), "exact tip re-announce")
}

// A reorg that displaces several blocks drops the whole suffix top-down (highest
// height first) before announcing the new block.
func TestBlockTrackerDeepBackwardReorgDropsSuffixTopDown(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(3, "cc", "bb"))
	tr.advance(blk(4, "dd", "cc"))
	tr.advance(blk(5, "ee", "dd")) // tip = 5

	got := tr.advance(blk(2, "b2", "aa")) // reorg at height 2
	assertUpdates(t, got, []wantUpdate{
		{5, "ee", BlockDrop},
		{4, "dd", BlockDrop},
		{3, "cc", BlockDrop},
		{2, "bb", BlockDrop},
		{2, "b2", BlockNew},
	})
}

// When the dropped suffix spans heights that were never announced (a prior gap),
// only the announced ones are dropped.
func TestBlockTrackerReorgSuffixSkipsUnannouncedHeights(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(5, "ee", "dd")) // gap: heights 3,4 never announced. tip = 5

	got := tr.advance(blk(2, "b2", "aa")) // reorg at 2
	assertUpdates(t, got, []wantUpdate{
		{5, "ee", BlockDrop}, // 4 and 3 were never announced -> skipped
		{2, "bb", BlockDrop},
		{2, "b2", BlockNew},
	})
}

// A lower head at a height we never announced (e.g. below a gap) is ignored: no
// DROP and no backfill.
func TestBlockTrackerLowerHeadUnannouncedHeightSkipped(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(3, "cc", "bb")) // gap: height 2 never announced. tip = 3

	noUpdate(t, tr, blk(2, "xx", "aa"), "unannounced lower height")
	// tip is unchanged, so a real extension still works.
	assertUpdates(t, tr.advance(blk(4, "dd", "cc")), []wantUpdate{{4, "dd", BlockNew}})
}

// A reorg older than the history window cannot be reconciled (its entry has been
// evicted), so it is skipped rather than mis-detected.
func TestBlockTrackerReorgBelowWindowSkipped(t *testing.T) {
	tr := newBlockTracker()
	parent := "00"
	for h := uint64(1); h <= historyRingSize+5; h++ {
		cur := hexFor(h)
		tr.advance(blk(h, cur, parent))
		parent = cur
	}
	// Height 3 is far below the retained window (its ring slot now holds 3+N).
	noUpdate(t, tr, blk(3, "zz", "02"), "reorg older than the window")
}

// After a reorg the tip moves down to the reorg point and the new chain extends
// cleanly from there; the orphaned hashes are gone from history.
func TestBlockTrackerReorgThenNewChainExtends(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(3, "cc", "bb")) // tip = 3

	assertUpdates(t, tr.advance(blk(2, "b2", "aa")), []wantUpdate{
		{3, "cc", BlockDrop}, {2, "bb", BlockDrop}, {2, "b2", BlockNew},
	})
	// new chain continues from b2@2
	assertUpdates(t, tr.advance(blk(3, "c2", "b2")), []wantUpdate{{3, "c2", BlockNew}})
	assertUpdates(t, tr.advance(blk(4, "d2", "c2")), []wantUpdate{{4, "d2", BlockNew}})
}

// Two forward reorgs in a row, each replacing the freshly-announced tip.
func TestBlockTrackerSequentialForwardReorgs(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	tr.advance(blk(3, "cc", "bb")) // tip = 3

	assertUpdates(t, tr.advance(blk(4, "dd", "c2")), []wantUpdate{ // 4 builds on c2 != cc
		{3, "cc", BlockDrop}, {4, "dd", BlockNew},
	})
	assertUpdates(t, tr.advance(blk(5, "ee", "d2")), []wantUpdate{ // 5 builds on d2 != dd
		{4, "dd", BlockDrop}, {5, "ee", BlockNew},
	})
}

// A forward reorg at the tip right after a gap drops the gapped tip and announces
// the new successor.
func TestBlockTrackerForwardReorgAfterGap(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(5, "ee", "dd")) // gap. tip = 5
	got := tr.advance(blk(6, "ff", "e2"))
	assertUpdates(t, got, []wantUpdate{{5, "ee", BlockDrop}, {6, "ff", BlockNew}})
}

// An empty-hash head mid-stream is ignored without corrupting tracker state.
func TestBlockTrackerEmptyHashDoesNotCorruptState(t *testing.T) {
	tr := newBlockTracker()
	tr.advance(blk(1, "aa", "00"))
	tr.advance(blk(2, "bb", "aa"))
	noUpdate(t, tr, protocol.Block{Height: 3}, "empty hash mid-stream")
	assertUpdates(t, tr.advance(blk(3, "cc", "bb")), []wantUpdate{{3, "cc", BlockNew}})
}

// A reorg at height 0 must not underflow the descending drop loop.
func TestBlockTrackerReorgAtHeightZero(t *testing.T) {
	tr := newBlockTracker()
	assertUpdates(t, tr.advance(blk(0, "aa", "00")), []wantUpdate{{0, "aa", BlockNew}})
	got := tr.advance(blk(0, "a0", "00"))
	assertUpdates(t, got, []wantUpdate{{0, "aa", BlockDrop}, {0, "a0", BlockNew}})
}

// dropFrom clamps the drop range to the last historyRingSize heights even when
// asked to start below the retained window.
func TestDropFromClampedToWindow(t *testing.T) {
	tr := newBlockTracker()
	const base = uint64(100)
	for h := base; h <= base+historyRingSize-1; h++ {
		tr.put(blk(h, hexFor(h), "00"))
	}
	tr.haveTip = true
	tr.tipH = base + historyRingSize - 1

	drops := tr.dropFrom(1) // far below the window
	require.Len(t, drops, historyRingSize)
	assert.Equal(t, tr.tipH, drops[0].Block.Height)         // top-down
	assert.Equal(t, base, drops[len(drops)-1].Block.Height) // clamped lower bound
}

// dropFrom from height 0 drops the single entry and stops without underflow.
func TestDropFromHeightZeroNoUnderflow(t *testing.T) {
	tr := newBlockTracker()
	tr.put(blk(0, "aa", "00"))
	tr.haveTip = true
	tr.tipH = 0
	drops := tr.dropFrom(0)
	require.Len(t, drops, 1)
	assert.Equal(t, uint64(0), drops[0].Block.Height)
}

// hexFor returns a short, unique, valid hex string for a height.
func hexFor(h uint64) string {
	const digits = "0123456789abcdef"
	if h == 0 {
		return "00"
	}
	out := ""
	for h > 0 {
		out = string(digits[h&0xf]) + out
		h >>= 4
	}
	if len(out)%2 != 0 {
		out = "0" + out
	}
	return out
}
