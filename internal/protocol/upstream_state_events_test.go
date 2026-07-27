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

	// Same is always false and ProcessEvent is a no-op: the setStatus/setLag
	// derivation lives in the upstream event loop (see upstream_test.go), which
	// owns the base availability and the per-chain syncing threshold.
	assert.False(t, event.Same(state))
	assert.Equal(t, state, event.ProcessEvent(state))
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

func TestCapsUpstreamStateEvent(t *testing.T) {
	t.Run("replaces the cap set wholesale", func(t *testing.T) {
		state := protocol.DefaultUpstreamState(
			mocks.NewMethodsMock(),
			mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
			"u", nil, nil,
		)
		event := &protocol.CapsUpstreamStateEvent{
			Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap),
		}

		next := event.ProcessEvent(state)

		assert.True(t, next.Caps.Contains(protocol.WsCap))
		assert.True(t, next.Caps.Contains(protocol.NewHeadsCap))
		// PendingTxCap was in the old set but not the new one -> dropped.
		assert.False(t, next.Caps.Contains(protocol.PendingTxCap))
		// the event carries a clone, mutating it must not touch upstream state
		assert.NotSame(t, event.Caps, next.Caps)
	})

	t.Run("empty caps clears everything", func(t *testing.T) {
		state := protocol.DefaultUpstreamState(
			mocks.NewMethodsMock(),
			mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap, protocol.PendingTxCap),
			"u", nil, nil,
		)
		event := &protocol.CapsUpstreamStateEvent{Caps: mapset.NewThreadUnsafeSet[protocol.Cap]()}

		next := event.ProcessEvent(state)

		assert.Equal(t, 0, next.Caps.Cardinality())
	})

	t.Run("Same is true only for an equal set", func(t *testing.T) {
		state := protocol.DefaultUpstreamState(
			mocks.NewMethodsMock(),
			mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
			"u", nil, nil,
		)

		same := &protocol.CapsUpstreamStateEvent{
			Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
		}
		different := &protocol.CapsUpstreamStateEvent{
			Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap),
		}

		assert.True(t, same.Same(state))
		assert.False(t, different.Same(state))
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
