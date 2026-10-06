package protocol_test

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func groupKeyState(names ...string) *protocol.UpstreamState {
	methods := mocks.NewMethodsMock()
	methods.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet(names...))
	for _, name := range names {
		methods.On("GetMethod", name).Return(specs.GetSpecMethod("eth", name))
	}
	state := protocol.DefaultUpstreamState(methods, mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	return &state
}

func TestNodeGroupKeyAndSelector(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	a, b := groupKeyState("eth_call", "eth_getBalance"), groupKeyState("eth_getBalance", "eth_call", "eth_subscribe")
	a.Labels.AddLabel("client_type", "geth")
	a.Labels.AddLabel("archive", "true")
	b.Labels.AddLabel("archive", "true")
	b.Labels.AddLabel("client_type", "geth")
	id := protocol.NodeGroupID("a", a, false)
	assert.Equal(t, id, protocol.NodeGroupID("b", b, false), "subscription operations and input ordering cannot split groups")
	assert.NotEqual(t, protocol.NodeGroupID("a", a, true), protocol.NodeGroupID("b", b, true))
	pins, rest := protocol.SplitUpstreamPins([]protocol.RequestSelector{protocol.RequestLabelSelector{Name: protocol.NodeGroupLabel, Values: []string{id}}})
	assert.Empty(t, rest)
	require.True(t, pins.Matches("b", b))
	b.Labels.AddLabel("archive", "false")
	assert.False(t, pins.Matches("b", b), "old selector cannot execute on a moved member")
	assert.True(t, pins.Matches("a", a))
	assert.False(t, pins.Matches("missing", nil))
	for _, values := range [][]string{nil, {id, protocol.NodeGroupID("b", b, false)}, {"missing"}} {
		gate, _ := protocol.SplitUpstreamPins([]protocol.RequestSelector{protocol.RequestLabelSelector{Name: protocol.NodeGroupLabel, Values: values}})
		assert.True(t, gate.Pinned())
		assert.False(t, gate.Matches("a", a))
	}
	singleton, _ := protocol.SplitUpstreamPins([]protocol.RequestSelector{protocol.RequestLabelSelector{Name: protocol.NodeGroupLabel, Values: []string{protocol.NodeGroupID("a", a, true)}}})
	assert.True(t, singleton.Matches("a", a))
	assert.False(t, singleton.Matches("b", a))
}

func TestGroupSelectorCannotBeWeakened(t *testing.T) {
	group := protocol.RequestLabelSelector{Name: protocol.NodeGroupLabel, Values: []string{"g:old"}}
	for _, selector := range []protocol.RequestSelector{
		protocol.RequestOrSelector{Children: []protocol.RequestSelector{group, protocol.RequestAnySelector{}}},
		protocol.RequestNotSelector{Child: group},
	} {
		pins, _ := protocol.SplitUpstreamPins([]protocol.RequestSelector{selector})
		require.True(t, pins.Pinned())
		assert.False(t, pins.Matches("a", groupKeyState("eth_call")))
	}
}

func TestGroupKeyCacheInvalidatesOnStatePublication(t *testing.T) {
	original := groupKeyState("eth_call").WithNodeGroupCache()
	id := protocol.NodeGroupID("a", original, false)
	next := *original
	next.Labels = protocol.NewLabels()
	next.Labels.AddLabel("client_type", "erigon")
	updated := next.WithNodeGroupCache()
	assert.NotEqual(t, id, protocol.NodeGroupID("a", updated, false))
	assert.Equal(t, id, protocol.NodeGroupID("a", original, false))
}
