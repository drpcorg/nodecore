package protocol_test

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func newUpstreamState() protocol.UpstreamState {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("HasMethod", mock.Anything).Return(false).Maybe()
	return protocol.DefaultUpstreamState(
		methodsMock,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"test-upstream",
		nil,
		nil,
	)
}

func TestLowerBoundUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.LowerBoundUpstreamStateEvent{
		Data: protocol.NewLowerBoundData(42, 100, protocol.BlockBound),
	}

	assert.False(t, event.Same(state))

	nextState := event.ProcessEvent(state)
	bound, ok := nextState.LowerBoundsInfo.GetLowerBound(protocol.BlockBound)
	_, originalHasBound := state.LowerBoundsInfo.GetLowerBound(protocol.BlockBound)

	assert.False(t, originalHasBound)
	assert.True(t, ok)
	assert.Equal(t, event.Data, bound)
	assert.NotSame(t, state.LowerBoundsInfo, nextState.LowerBoundsInfo)
	assert.True(t, event.Same(nextState))
}

func TestStatusUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.StatusUpstreamStateEvent{Status: protocol.Syncing}

	assert.False(t, event.Same(state))

	nextState := event.ProcessEvent(state)

	assert.Equal(t, protocol.Available, state.Status)
	assert.Equal(t, protocol.Syncing, nextState.Status)
	assert.NotEqual(t, state.Status, nextState.Status)
	assert.True(t, event.Same(nextState))
}

func TestFatalErrorUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.FatalErrorUpstreamStateEvent{}

	assert.False(t, event.Same(state))
	assert.Equal(t, state, event.ProcessEvent(state))
}

func TestValidUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.ValidUpstreamStateEvent{}

	assert.False(t, event.Same(state))
	assert.Equal(t, state, event.ProcessEvent(state))
}

func TestHeadUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	head := protocol.NewBlockWithHeights(100, 200)
	event := &protocol.HeadUpstreamStateEvent{HeadData: head}

	assert.False(t, event.Same(state))

	nextState := event.ProcessEvent(state)

	assert.True(t, protocol.ZeroBlock{}.Equals(state.HeadData))
	assert.True(t, head.Equals(nextState.HeadData))
	assert.False(t, state.HeadData.Equals(nextState.HeadData))
}

func TestBlockUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	block := protocol.NewBlockWithHeights(10, 20)
	event := &protocol.BlockUpstreamStateEvent{
		Block:     block,
		BlockType: protocol.FinalizedBlock,
	}

	assert.False(t, event.Same(state))

	nextState := event.ProcessEvent(state)

	assert.True(t, protocol.Block{}.Equals(state.BlockInfo.GetBlock(protocol.FinalizedBlock)))
	assert.True(t, block.Equals(nextState.BlockInfo.GetBlock(protocol.FinalizedBlock)))
	assert.NotSame(t, state.BlockInfo, nextState.BlockInfo)
	assert.True(t, event.Same(nextState))
}

func TestBanMethodUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.BanMethodUpstreamStateEvent{Method: "eth_call"}

	assert.False(t, event.Same(state))
	assert.Equal(t, state, event.ProcessEvent(state))
}

func TestUnbanMethodUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.UnbanMethodUpstreamStateEvent{Method: "eth_call"}

	assert.False(t, event.Same(state))
	assert.Equal(t, state, event.ProcessEvent(state))
}

func TestSubscribeUpstreamStateEvent(t *testing.T) {
	t.Run("ws connected adds capability", func(t *testing.T) {
		state := newUpstreamState()
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected}

		assert.False(t, event.Same(state))

		nextState := event.ProcessEvent(state)

		assert.False(t, state.Caps.Contains(protocol.WsCap))
		assert.True(t, nextState.Caps.Contains(protocol.WsCap))
		assert.NotSame(t, state.Caps, nextState.Caps)
		assert.True(t, event.Same(nextState))
	})

	t.Run("ws disconnected removes capability", func(t *testing.T) {
		state := newUpstreamState()
		state.Caps.Add(protocol.WsCap)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsDisconnected}

		assert.False(t, event.Same(state))

		nextState := event.ProcessEvent(state)

		assert.True(t, state.Caps.Contains(protocol.WsCap))
		assert.False(t, nextState.Caps.Contains(protocol.WsCap))
		assert.NotSame(t, state.Caps, nextState.Caps)
		assert.True(t, event.Same(nextState))
	})

	t.Run("unexpected state returns false in Same", func(t *testing.T) {
		state := newUpstreamState()
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.SubscribeConnectorState(99)}

		assert.False(t, event.Same(state))
	})

	t.Run("evm head connector ws connect adds newHeads and logs caps", func(t *testing.T) {
		methodsMock := mocks.NewMethodsMock()
		methodsMock.On("HasMethod", "eth_subscribe").Return(true)
		methodsMock.On("HasMethod", "eth_getLogs").Return(true)
		state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "u", nil, nil)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected, HeadConnector: true}

		next := event.ProcessEvent(state)

		assert.True(t, next.Caps.Contains(protocol.WsCap))
		assert.True(t, next.Caps.Contains(protocol.NewHeadsCap))
		assert.True(t, next.Caps.Contains(protocol.LogsCap))
	})

	t.Run("evm head connector ws connect without eth_getLogs has no logs cap", func(t *testing.T) {
		methodsMock := mocks.NewMethodsMock()
		methodsMock.On("HasMethod", "eth_subscribe").Return(true)
		methodsMock.On("HasMethod", "eth_getLogs").Return(false)
		state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "u", nil, nil)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected, HeadConnector: true}

		next := event.ProcessEvent(state)

		assert.True(t, next.Caps.Contains(protocol.NewHeadsCap))
		assert.False(t, next.Caps.Contains(protocol.LogsCap))
	})

	// A ws connector used only for requests (head connector is e.g. json-rpc)
	// must not grant local-sub caps - otherwise newHeads routes to local
	// synthesis with a polled head and emits nothing.
	t.Run("non-head ws connect grants only WsCap", func(t *testing.T) {
		methodsMock := mocks.NewMethodsMock()
		methodsMock.On("HasMethod", "eth_subscribe").Return(true).Maybe()
		state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "u", nil, nil)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected, HeadConnector: false}

		next := event.ProcessEvent(state)

		assert.True(t, next.Caps.Contains(protocol.WsCap))
		assert.False(t, next.Caps.Contains(protocol.NewHeadsCap))
		assert.False(t, next.Caps.Contains(protocol.LogsCap))
	})

	t.Run("non-evm head connector ws connect grants only WsCap", func(t *testing.T) {
		methodsMock := mocks.NewMethodsMock()
		methodsMock.On("HasMethod", "eth_subscribe").Return(false)
		state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "u", nil, nil)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected, HeadConnector: true}

		next := event.ProcessEvent(state)

		assert.True(t, next.Caps.Contains(protocol.WsCap))
		assert.False(t, next.Caps.Contains(protocol.NewHeadsCap))
		assert.False(t, next.Caps.Contains(protocol.LogsCap))
	})

	t.Run("ws disconnect removes all sub caps", func(t *testing.T) {
		state := protocol.DefaultUpstreamState(
			mocks.NewMethodsMock(),
			mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap),
			"u", nil, nil,
		)
		event := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsDisconnected}

		next := event.ProcessEvent(state)

		assert.False(t, next.Caps.Contains(protocol.WsCap))
		assert.False(t, next.Caps.Contains(protocol.NewHeadsCap))
		assert.False(t, next.Caps.Contains(protocol.LogsCap))
	})
}

func TestLabelsUpstreamStateEvent(t *testing.T) {
	state := newUpstreamState()
	event := &protocol.LabelsUpstreamStateEvent{
		Labels: lo.T2("region", "us-east-1"),
	}

	assert.False(t, event.Same(state))

	nextState := event.ProcessEvent(state)
	label, ok := nextState.Labels.GetLabel("region")
	_, originalHasLabel := state.Labels.GetLabel("region")

	assert.False(t, originalHasLabel)
	assert.True(t, ok)
	assert.Equal(t, "us-east-1", label)
	assert.NotSame(t, state.Labels, nextState.Labels)
	assert.True(t, event.Same(nextState))
}
