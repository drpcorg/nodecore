package caps

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTracker() (*headLivenessTracker, *fakeClock) {
	clk := &fakeClock{t: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	return &headLivenessTracker{now: clk.now}, clk
}

// driveLiveByCount feeds a fresh consecutive run of checkedBlocksUntilLive blocks
// starting at `start` (clock left static) so the block-count path flips the tracker
// live. Returns the last height emitted.
func driveLiveByCount(tr *headLivenessTracker, start uint64) uint64 {
	for i := 0; i < checkedBlocksUntilLive; i++ {
		tr.observe(start + uint64(i))
	}
	return start + uint64(checkedBlocksUntilLive-1)
}

// TestHeadLivenessTracker exercises the headLivenessTracker reducer directly.
func TestHeadLivenessTracker(t *testing.T) {
	t.Run("first observation is neutral and stays not live", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100))
	})

	t.Run("becomes live after checkedBlocksUntilLive consecutive blocks", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		for h := uint64(101); h < 100+checkedBlocksUntilLive-1; h++ {
			assert.False(t, tr.observe(h), "not live at height %d", h)
		}
		assert.True(t, tr.observe(100+checkedBlocksUntilLive-1))
	})

	t.Run("becomes live via the time cap before the block count is reached", func(t *testing.T) {
		tr, clk := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		clk.advance(maxWarmup)
		assert.True(t, tr.observe(101), "one consecutive block after maxWarmup -> live")
	})

	t.Run("stays live while it keeps advancing consecutively", func(t *testing.T) {
		tr, _ := newTracker()
		last := driveLiveByCount(tr, 100)
		assert.True(t, tr.live)
		assert.True(t, tr.observe(last+1))
		assert.True(t, tr.observe(last+2))
	})

	t.Run("duplicate heights are neutral and never advance liveness", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		assert.False(t, tr.observe(100)) // duplicate
		assert.False(t, tr.observe(100)) // duplicate
		assert.Equal(t, 0, tr.count)
	})

	t.Run("a duplicate does not reset progress toward liveness", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		assert.False(t, tr.observe(101)) // 1 consecutive
		assert.Equal(t, 1, tr.count)
		assert.False(t, tr.observe(101)) // duplicate -> neutral, count kept
		assert.Equal(t, 1, tr.count)
	})

	t.Run("a gap resets progress and retracts liveness, then recovers", func(t *testing.T) {
		tr, _ := newTracker()
		driveLiveByCount(tr, 100)
		assert.True(t, tr.live)

		assert.False(t, tr.observe(1000)) // gap (diff >= 2) -> not live, new baseline

		var live bool
		for h := uint64(1001); h <= 1000+uint64(checkedBlocksUntilLive-1); h++ {
			live = tr.observe(h)
		}
		assert.True(t, live, "recovers after a fresh consecutive run")
	})

	t.Run("a gap resets the time reference too", func(t *testing.T) {
		tr, clk := newTracker()
		last := driveLiveByCount(tr, 100)
		assert.True(t, tr.live)

		assert.False(t, tr.observe(last+10)) // gap -> reset
		clk.advance(maxWarmup)
		assert.True(t, tr.observe(last+11), "time cap applies from the gap onward")
	})

	t.Run("a backward reorg resets progress and retracts liveness", func(t *testing.T) {
		tr, _ := newTracker()
		driveLiveByCount(tr, 100)
		assert.True(t, tr.live)
		assert.False(t, tr.observe(50)) // backward -> not live
	})

	t.Run("a gap right after baseline keeps it not live", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100))
		assert.False(t, tr.observe(200)) // large gap -> reset, new baseline

		var live bool
		for h := uint64(201); h <= 200+uint64(checkedBlocksUntilLive-1); h++ {
			live = tr.observe(h)
		}
		assert.True(t, live)
	})
}
