package caps_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bigBlockTime makes the stall-timeout window effectively unreachable within a test, so the
// block-count / gap paths (real clock) decide the outcome without the timer interfering.
const bigBlockTime = time.Hour

// headFeed is a caps.HeadSource backed by a real SubscriptionManager so tests can push
// synthetic heads into the detector.
type headFeed struct {
	mgr *utils.SubscriptionManager[blocks.HeadEvent]
}

func newHeadFeed() *headFeed {
	return &headFeed{mgr: utils.NewSubscriptionManager[blocks.HeadEvent]("test_heads")}
}

func (h *headFeed) Subscribe(name string) *utils.Subscription[blocks.HeadEvent] {
	return h.mgr.Subscribe(name)
}

func (h *headFeed) emit(height uint64) {
	h.mgr.Publish(blocks.HeadEvent{HeadData: protocol.NewBlockWithHeight(height)})
}

func nextCaps(t *testing.T, out <-chan mapset.Set[protocol.Cap]) mapset.Set[protocol.Cap] {
	t.Helper()
	select {
	case c, ok := <-out:
		require.True(t, ok, "cap channel closed unexpectedly")
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a cap snapshot")
		return nil
	}
}

// driveToLive emits consecutive heights starting at `start`, draining each cap snapshot, until
// WsCap is asserted. It is threshold-agnostic (the detector uses the real clock, so blocks
// arrive far faster than the stall timeout and the block-count path decides). Returns the last
// height emitted so the caller can continue the consecutive run.
func driveToLive(t *testing.T, source *headFeed, out <-chan mapset.Set[protocol.Cap], start uint64) uint64 {
	t.Helper()
	h := start
	for i := 0; i < 64; i++ {
		source.emit(h)
		if nextCaps(t, out).Contains(protocol.WsCap) {
			return h
		}
		h++
	}
	t.Fatal("head never went live after 64 consecutive blocks")
	return 0
}

func TestWsHeadLivenessCapDetector(t *testing.T) {
	t.Run("asserts the cap only when connected AND head is consecutive", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap), "connected but no head yet")

		source := head
		source.emit(100) // baseline
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap))
		source.emit(101) // 1 consecutive - below the threshold
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap))

		driveToLive(t, source, out, 102) // finish the consecutive run -> live
	})

	t.Run("ws disconnect retracts the cap even while heads keep flowing", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		nextCaps(t, out)
		last := driveToLive(t, head, out, 100)

		wsMgr.Publish(protocol.WsDisconnected)
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap))

		head.emit(last + 1) // head still live, but disconnected -> no cap
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap))

		wsMgr.Publish(protocol.WsConnected) // reconnect -> live again
		assert.True(t, nextCaps(t, out).Contains(protocol.WsCap))
	})

	t.Run("a forward gap while connected retracts the cap", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		nextCaps(t, out)
		last := driveToLive(t, head, out, 100)

		head.emit(last + 3) // forward gap -> not live (recovery is gated by the 5m cooldown)
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap))
	})

	t.Run("a backward reorg keeps the cap (diff <= 1 is consecutive)", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		nextCaps(t, out)
		driveToLive(t, head, out, 100)

		head.emit(99) // backward/reorg -> still consecutive, stays live
		assert.True(t, nextCaps(t, out).Contains(protocol.WsCap))
	})

	t.Run("duplicate height while live keeps the cap", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		nextCaps(t, out)
		last := driveToLive(t, head, out, 100)

		head.emit(last) // duplicate height -> still consecutive, stays live
		assert.True(t, nextCaps(t, out).Contains(protocol.WsCap))
	})

	t.Run("a stalled head times out and retracts the cap", func(t *testing.T) {
		conn, wsMgr := stateFeed("ws")
		head := newHeadFeed()
		// Small expected block time -> timeout window ~= 100ms * 3 * 2 = 600ms, so a stall is
		// observable within the test.
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, head, 100*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		wsMgr.Publish(protocol.WsConnected)
		nextCaps(t, out)
		driveToLive(t, head, out, 100) // live

		// Stop emitting; the stall timeout must fire and retract the cap (within nextCaps' 2s budget).
		assert.False(t, nextCaps(t, out).Contains(protocol.WsCap), "expected the cap to drop after a stall")
	})

	t.Run("nil head source never asserts the cap", func(t *testing.T) {
		conn, _ := stateFeed("ws")
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, conn, nil, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok, "expected a closed channel when there is no head source")
	})

	t.Run("nil ws connector never asserts the cap", func(t *testing.T) {
		head := newHeadFeed()
		detector := caps.NewWsHeadLivenessCapDetector("up", "ws", protocol.WsCap, nil, head, bigBlockTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok, "expected a closed channel for an upstream without a ws connector")
	})
}
