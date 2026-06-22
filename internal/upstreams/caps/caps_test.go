package caps_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func capSet(c ...protocol.Cap) mapset.Set[protocol.Cap] {
	return mapset.NewThreadUnsafeSet[protocol.Cap](c...)
}

// stateFeed wires a ConnectorMock so SubscribeStates returns a subscription the test
// can publish into.
func stateFeed(name string) (*mocks.ConnectorMock, *utils.SubscriptionManager[protocol.SubscribeConnectorState]) {
	mgr := utils.NewSubscriptionManager[protocol.SubscribeConnectorState]("test_states")
	sub := mgr.Subscribe(name)
	conn := mocks.NewConnectorMock()
	conn.On("SubscribeStates", name).Return(sub)
	return conn, mgr
}

func TestWsPresenceCapDetector(t *testing.T) {
	t.Run("asserts the cap on connect and retracts on disconnect", func(t *testing.T) {
		conn, mgr := stateFeed("ws")
		detector := caps.NewWsPresenceCapDetector("ws", protocol.WsCap, conn)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.True(t, (<-out).Contains(protocol.WsCap))

		mgr.Publish(protocol.WsDisconnected)
		assert.False(t, (<-out).Contains(protocol.WsCap))
	})

	t.Run("nil connector never asserts the cap", func(t *testing.T) {
		detector := caps.NewWsPresenceCapDetector("ws", protocol.WsCap, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok, "expected a closed channel for an upstream without a ws connector")
	})

	t.Run("connector without a state stream never asserts the cap", func(t *testing.T) {
		conn := mocks.NewConnectorMock()
		conn.On("SubscribeStates", "ws").Return(nil)
		detector := caps.NewWsPresenceCapDetector("ws", protocol.WsCap, conn)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok)
	})
}

// scriptedDetector is a CapDetector whose snapshots the test pushes by hand.
type scriptedDetector struct {
	domain []protocol.Cap
	ch     chan mapset.Set[protocol.Cap]
}

func (s *scriptedDetector) Domain() []protocol.Cap { return s.domain }
func (s *scriptedDetector) DetectCaps(_ context.Context) <-chan mapset.Set[protocol.Cap] {
	return s.ch
}

func TestBaseCapProcessor(t *testing.T) {
	t.Run("nil when there are no detectors", func(t *testing.T) {
		assert.Nil(t, caps.NewBaseCapProcessor(context.Background(), "u", nil))
	})

	t.Run("merges disjoint detectors and publishes on change only", func(t *testing.T) {
		ws := &scriptedDetector{domain: []protocol.Cap{protocol.WsCap}, ch: make(chan mapset.Set[protocol.Cap], 1)}
		pt := &scriptedDetector{domain: []protocol.Cap{protocol.PendingTxCap}, ch: make(chan mapset.Set[protocol.Cap], 1)}

		proc := caps.NewBaseCapProcessor(context.Background(), "u", []caps.CapDetector{ws, pt})
		require.NotNil(t, proc)

		sub := proc.Subscribe("s")
		proc.Start()
		defer proc.Stop()

		ws.ch <- capSet(protocol.WsCap)
		got := <-sub.Events
		assert.True(t, got.Contains(protocol.WsCap))
		assert.False(t, got.Contains(protocol.PendingTxCap))

		pt.ch <- capSet(protocol.PendingTxCap)
		got = <-sub.Events
		assert.True(t, got.Contains(protocol.WsCap))
		assert.True(t, got.Contains(protocol.PendingTxCap))

		// An identical snapshot must not produce a new merged event.
		pt.ch <- capSet(protocol.PendingTxCap)
		assertNoEvent(t, sub)

		// Dropping ws retracts only WsCap; PendingTxCap survives.
		ws.ch <- capSet()
		got = <-sub.Events
		assert.False(t, got.Contains(protocol.WsCap))
		assert.True(t, got.Contains(protocol.PendingTxCap))
	})
}

func assertNoEvent(t *testing.T, sub *utils.Subscription[mapset.Set[protocol.Cap]]) {
	t.Helper()
	select {
	case got := <-sub.Events:
		t.Fatalf("expected no new cap event, got %v", got.ToSlice())
	case <-time.After(150 * time.Millisecond):
	}
}
