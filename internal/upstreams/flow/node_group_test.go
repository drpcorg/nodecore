package flow

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gethUpstreamState() protocol.UpstreamState {
	state := protocol.DefaultUpstreamState(nil, nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "geth")
	return state
}

func TestNodeGroupMatcherMatchesTheDerivedId(t *testing.T) {
	state := gethUpstreamState()
	id := upstreams.CachedNodeGroupId(upstreams.SeparationGroups, "up", &state)

	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher([]string{id}).Match("up", &state))
	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher([]string{"g:erigon:dead:beef", id}).Match("up", &state))
	assert.IsType(t, NodeGroupResponse{}, NewNodeGroupMatcher([]string{"g:erigon:dead:beef"}).Match("up", &state))
}

func TestNodeGroupMatcherEdgeCases(t *testing.T) {
	state := gethUpstreamState()

	// an empty value list is "any group", the same rule LabelMatcher follows
	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher(nil).Match("up", &state))
	// no state means the upstream cannot be proven to be in the group
	assert.IsType(t, NodeGroupResponse{}, NewNodeGroupMatcher([]string{"g:geth:dead:beef"}).Match("up", nil))
}

func TestNodeGroupMatcherFollowsTheUpstreamOutOfTheGroup(t *testing.T) {
	state := gethUpstreamState()
	id := upstreams.CachedNodeGroupId(upstreams.SeparationGroups, "up", &state)
	matcher := NewNodeGroupMatcher([]string{id})
	require.IsType(t, SuccessResponse{}, matcher.Match("up", &state))

	// a label change moves the upstream to another group; a request pinned to the
	// old one must stop matching instead of landing on a node that left it
	moved := gethUpstreamState()
	moved.Labels.AddLabel("archive", "true")
	assert.IsType(t, NodeGroupResponse{}, matcher.Match("up", &moved))
}

func TestNodeGroupSelectorCompilesToItsOwnMatcher(t *testing.T) {
	pin := protocol.RequestLabelSelector{Name: upstreams.NodeGroupLabel, Values: []string{"g:geth:dead:beef"}}

	// a pin is the selection gate, not a matcher
	matchers, order := buildSelectorRouting([]protocol.RequestSelector{pin}, nil, nil)
	assert.Empty(t, matchers)
	assert.Nil(t, order)

	// under OR it is an ordinary matcher
	matchers, _ = buildSelectorRouting([]protocol.RequestSelector{
		protocol.RequestOrSelector{Children: []protocol.RequestSelector{pin}},
	}, nil, nil)
	require.Len(t, matchers, 1)
	require.IsType(t, &SelectorOrMatcher{}, matchers[0])
	assert.IsType(t, &NodeGroupMatcher{}, matchers[0].(*SelectorOrMatcher).matchers[0])

	// every other label keeps the generic matcher
	matchers, _ = buildSelectorRouting([]protocol.RequestSelector{
		protocol.RequestLabelSelector{Name: "client_type", Values: []string{"geth"}},
	}, nil, nil)
	require.Len(t, matchers, 1)
	assert.IsType(t, &LabelMatcher{}, matchers[0])
}

// A group-scoped subscription cannot ride a chain-wide synthesized source: it
// has one head and one mempool for the whole chain.
func TestResolveSourceFallsThroughForGroupScopedSubscriptions(t *testing.T) {
	groupSelector := protocol.RequestLabelSelector{Name: upstreams.NodeGroupLabel, Values: []string{"g:geth:dead:beef"}}

	pinned := func(params string) protocol.RequestHolder {
		return protocol.NewUpstreamJsonRpcRequest("1",
			protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(params)}, true, "eth", groupSelector)
	}
	resolve := func(req protocol.RequestHolder) string {
		key, _, _ := resolveSource(chains.ETHEREUM, allCapsSupervisor(), req, nil, nil, nil, allLocalSubs)
		return key
	}

	assert.NotEqual(t, localNewHeadsKey, resolve(pinned(`["newHeads"]`)))
	assert.NotEqual(t, localPendingTxKey, resolve(pinned(`["newPendingTransactions"]`)))
	assert.NotEqual(t, localLogsKey, resolve(pinned(`["logs",{}]`)))

	// without a selector the shared local sources stay in use
	assert.Equal(t, localNewHeadsKey, resolve(subscribeRequest(`["newHeads"]`)))
	assert.Equal(t, localPendingTxKey, resolve(subscribeRequest(`["newPendingTransactions"]`)))

	// drpc_pendingTransactions has no node-backed equivalent: it stays local and
	// a pinned client drops the frames of upstreams outside its pin
	key, _, accept := resolveSource(chains.ETHEREUM, allCapsSupervisor(), pinned(`["drpc_pendingTransactions"]`), nil, nil, nil, allLocalSubs)
	assert.Equal(t, localDrpcPendingTxKey, key)
	require.NotNil(t, accept)
	assert.False(t, accept(&protocol.GenericSubResponse{UpstreamId: "up"}))
	_, _, accept = resolveSource(chains.ETHEREUM, allCapsSupervisor(), subscribeRequest(`["drpc_pendingTransactions"]`), nil, nil, nil, allLocalSubs)
	assert.Nil(t, accept)
}
