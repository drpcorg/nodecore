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
	id := upstreams.GroupKeyOf(&state).Id()

	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher([]string{id}).Match("up", &state))
	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher([]string{"erigon:dead:beef", id}).Match("up", &state))
	assert.IsType(t, LabelResponse{}, NewNodeGroupMatcher([]string{"erigon:dead:beef"}).Match("up", &state))
}

func TestNodeGroupMatcherEdgeCases(t *testing.T) {
	state := gethUpstreamState()

	// an empty value list is "any group", the same rule LabelMatcher follows
	assert.IsType(t, SuccessResponse{}, NewNodeGroupMatcher(nil).Match("up", &state))
	// no state means the upstream cannot be proven to be in the group
	assert.IsType(t, LabelResponse{}, NewNodeGroupMatcher([]string{"geth:dead:beef"}).Match("up", nil))
}

func TestNodeGroupMatcherFollowsTheUpstreamOutOfTheGroup(t *testing.T) {
	state := gethUpstreamState()
	id := upstreams.GroupKeyOf(&state).Id()
	matcher := NewNodeGroupMatcher([]string{id})
	require.IsType(t, SuccessResponse{}, matcher.Match("up", &state))

	// a label change moves the upstream to another group; a request pinned to the
	// old one must stop matching instead of landing on a node that left it
	moved := gethUpstreamState()
	moved.Labels.AddLabel("archive", "true")
	assert.IsType(t, LabelResponse{}, matcher.Match("up", &moved))
}

func TestNodeGroupSelectorCompilesToItsOwnMatcher(t *testing.T) {
	matchers, order := buildSelectorRouting([]protocol.RequestSelector{
		protocol.RequestLabelSelector{Name: upstreams.NodeGroupLabel, Values: []string{"geth:dead:beef"}},
	}, nil, nil)

	require.Len(t, matchers, 1)
	assert.IsType(t, &NodeGroupMatcher{}, matchers[0])
	assert.Nil(t, order)

	// every other label keeps the generic matcher
	matchers, _ = buildSelectorRouting([]protocol.RequestSelector{
		protocol.RequestLabelSelector{Name: "client_type", Values: []string{"geth"}},
	}, nil, nil)
	require.Len(t, matchers, 1)
	assert.IsType(t, &LabelMatcher{}, matchers[0])
}

// A group-scoped subscription cannot be served from the chain-wide synthesized
// source: that source has one head and one mempool for the whole chain and
// cannot honour a selector.
func TestResolveSourceFallsThroughForGroupScopedSubscriptions(t *testing.T) {
	groupSelector := protocol.RequestLabelSelector{Name: upstreams.NodeGroupLabel, Values: []string{"geth:dead:beef"}}

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

	// drpc_pendingTransactions has no node-backed equivalent, so it stays local
	assert.Equal(t, localDrpcPendingTxKey, resolve(pinned(`["drpc_pendingTransactions"]`)))
}
