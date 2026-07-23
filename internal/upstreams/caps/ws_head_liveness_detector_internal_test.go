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

// driveLiveByCount feeds a fresh consecutive run of checkedBlocksUntilLive blocks starting at
// `start` (clock left static, no prior gap so the cooldown does not apply) which flips the
// tracker live. Returns the last height emitted.
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

	t.Run("stays live while it keeps advancing consecutively", func(t *testing.T) {
		tr, _ := newTracker()
		last := driveLiveByCount(tr, 100)
		assert.True(t, tr.live)
		assert.True(t, tr.observe(last+1))
		assert.True(t, tr.observe(last+2))
	})

	t.Run("duplicate heights advance the run (diff <= 1 is consecutive)", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		assert.False(t, tr.observe(100)) // duplicate -> count 1
		assert.Equal(t, 1, tr.count)
		assert.True(t, tr.observe(100)) // duplicate -> count 2 -> live
	})

	t.Run("a backward reorg advances the run and does not retract", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100)) // baseline
		assert.False(t, tr.observe(50))  // backward -> count 1
		assert.Equal(t, 1, tr.count)
		assert.True(t, tr.observe(10)) // backward -> count 2 -> live
	})

	t.Run("a forward gap resets progress and retracts liveness, then recovers after cooldown", func(t *testing.T) {
		tr, clk := newTracker()
		driveLiveByCount(tr, 100)
		assert.True(t, tr.live)

		assert.False(t, tr.observe(1000)) // forward gap (diff >= 2) -> not live, cooldown starts
		assert.Equal(t, 0, tr.count)

		// A fresh consecutive run stays not-live while the cooldown is active.
		assert.False(t, tr.observe(1001)) // count 1
		assert.False(t, tr.observe(1002)) // count 2, but still in cooldown
		assert.False(t, tr.observe(1003)) // count 3, still in cooldown

		clk.advance(cooldown)
		assert.True(t, tr.observe(1004), "recovers once the cooldown has elapsed")
	})

	t.Run("a forward gap right after baseline keeps it not live until a run + no cooldown", func(t *testing.T) {
		tr, _ := newTracker()
		assert.False(t, tr.observe(100))
		assert.False(t, tr.observe(200)) // gap -> reset; cooldown starts from here (static clock)

		// Without advancing the clock the cooldown never clears, so it stays not-live.
		var live bool
		for h := uint64(201); h <= 200+uint64(checkedBlocksUntilLive-1); h++ {
			live = tr.observe(h)
		}
		assert.False(t, live)
	})

	t.Run("measuredBlockTime grows to the largest observed interval and never shrinks", func(t *testing.T) {
		tr, clk := newTracker()
		tr.measuredBlockTime = time.Second

		tr.observe(100) // baseline records lastBlockTime
		clk.advance(5 * time.Second)
		tr.observe(101) // interval 5s > 1s -> grow
		assert.Equal(t, 5*time.Second, tr.measuredBlockTime)

		clk.advance(2 * time.Second)
		tr.observe(102) // interval 2s < 5s -> unchanged
		assert.Equal(t, 5*time.Second, tr.measuredBlockTime)
	})

	t.Run("timeout is measuredBlockTime * checkedBlocksUntilLive * timeoutMultiplier", func(t *testing.T) {
		tr, _ := newTracker()
		tr.measuredBlockTime = 10 * time.Second
		assert.Equal(t, 10*time.Second*checkedBlocksUntilLive*timeoutMultiplier, tr.timeout())
	})

	t.Run("onTimeout retracts liveness and doubles measuredBlockTime, capped at maxBlockTime", func(t *testing.T) {
		tr, _ := newTracker()
		driveLiveByCount(tr, 100)
		assert.True(t, tr.live)

		tr.measuredBlockTime = time.Minute
		assert.False(t, tr.onTimeout())
		assert.False(t, tr.live)
		assert.Equal(t, 2*time.Minute, tr.measuredBlockTime)

		tr.measuredBlockTime = 8 * time.Minute
		tr.onTimeout() // 16m -> capped
		assert.Equal(t, maxBlockTime, tr.measuredBlockTime)
	})

	t.Run("a single consecutive block after a timeout flips it live again (no cooldown)", func(t *testing.T) {
		tr, _ := newTracker()
		last := driveLiveByCount(tr, 100)
		assert.True(t, tr.live)

		tr.onTimeout() // stall -> not live, but count and cooldown untouched
		assert.False(t, tr.live)
		assert.True(t, tr.observe(last+1), "recovers immediately, no cooldown from a timeout")
	})
}
