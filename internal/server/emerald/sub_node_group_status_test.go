package emerald_test

import (
	"slices"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type groupStatusStream struct {
	*subscribeChainStatusStream
	responses chan *dshackle.SubscribeNodeGroupStatusResponse
}

func (s *groupStatusStream) Send(response *dshackle.SubscribeNodeGroupStatusResponse) error {
	select {
	case s.responses <- proto.CloneOf(response):
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *groupStatusStream) next(t *testing.T) *dshackle.SubscribeNodeGroupStatusResponse {
	t.Helper()
	select {
	case response := <-s.responses:
		return response
	case <-time.After(time.Second):
		t.Fatal("no group response")
		return nil
	}
}
func startGroupStream(t *testing.T, chain *upstreamsChainSupervisor, full bool, resync time.Duration) *groupStatusStream {
	t.Helper()
	stream := &groupStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream(), responses: make(chan *dshackle.SubscribeNodeGroupStatusResponse, 100)}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeNodeGroupStatusWithResync(newChainsUpstreamSupervisor(chain), &dshackle.SubscribeNodeGroupStatusRequest{FullSeparation: full}, stream, time.Millisecond, resync)
	}()
	t.Cleanup(func() { stream.cancel(); <-done })
	return stream
}
func groupedTestChain(t *testing.T) *upstreamsChainSupervisor {
	t.Helper()
	loadMethodSpecs(t)
	chain := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chain.SetState(newChainState(chains.ETHEREUM, protocol.NewBlockWithHeight(200), []string{"eth_call"}))
	return chain
}

func TestNodeGroupsMembershipAndIndependentHeads(t *testing.T) {
	chain := groupedTestChain(t)
	a, b, c := testUpstreamState(100, "eth_call"), testUpstreamState(99, "eth_call"), testUpstreamState(200, "eth_getBalance")
	a.Labels.AddLabel("client_type", "erigon")
	b.Labels.AddLabel("client_type", "erigon")
	c.Labels.AddLabel("client_type", "geth")
	chain.set("a", a)
	chain.set("b", b)
	chain.set("c", c)
	groups := upstreams.NodeGroups(chain, false)
	require.Len(t, groups, 2)
	id := protocol.NodeGroupID("a", a, false)
	require.Len(t, groups[id].Indices, 2)
	assert.Equal(t, uint64(100), groups[id].State.HeadData.Head.Height)
	assert.Equal(t, uint64(200), chain.GetChainState().HeadData.Head.Height)
	assert.ElementsMatch(t, []string{"eth_call"}, groups[id].State.Methods.GetSupportedMethods().ToSlice())
	assert.Len(t, upstreams.NodeGroups(chain, true), 3)
	// Labels and methods independently move a member, and empty groups disappear.
	changed := testUpstreamState(99, "eth_getBalance")
	changed.Labels.AddLabel("client_type", "erigon")
	chain.set("b", changed)
	require.Len(t, upstreams.NodeGroups(chain, false), 3)
	chain.remove("a")
	assert.NotContains(t, upstreams.NodeGroups(chain, false), id)
	changed = testUpstreamState(99, "eth_getBalance")
	changed.Labels.AddLabel("client_type", "geth")
	chain.set("b", changed)
	require.Len(t, upstreams.NodeGroups(chain, false), 1)
	// A whole group downgraded by NETWORK validation stays Syncing despite its own head.
	chain.update("b", func(s *protocol.UpstreamState) { s.Status = protocol.Syncing })
	chain.update("c", func(s *protocol.UpstreamState) { s.Status = protocol.Syncing })
	groups = upstreams.NodeGroups(chain, false)
	for _, g := range groups {
		assert.Equal(t, protocol.Syncing, g.State.Status)
		assert.Empty(t, g.State.Methods.GetSupportedMethods().ToSlice())
	}
}

func TestNodeGroupStreamFullDeltaRemovalResync(t *testing.T) {
	chain := groupedTestChain(t)
	chain.set("a", testUpstreamState(100, "eth_call"))
	chain.set("b", testUpstreamState(99, "eth_call"))
	stream := startGroupStream(t, chain, false, time.Hour)
	first := stream.next(t)
	require.True(t, first.FullResponse)
	require.Len(t, first.Groups, 1)
	id := first.Groups[0].NodeGroupId
	assert.Len(t, first.Groups[0].UpstreamIndices, 2)
	require.NotNil(t, first.Network)
	chain.set("b", testUpstreamState(99, "eth_getBalance"))
	delta := stream.next(t)
	assert.False(t, delta.FullResponse)
	require.Len(t, delta.Groups, 2)
	for _, group := range delta.Groups {
		assert.NotEmpty(t, group.Description)
	}
	chain.remove("a")
	removed := stream.next(t)
	assert.Contains(t, removed.RemovedNodeGroupIds, id)
	chain.remove("b")
	removed = stream.next(t)
	assert.Len(t, removed.RemovedNodeGroupIds, 1)
	// A full resync contains the current partition, including singleton mode.
	chain.set("a", testUpstreamState(100, "eth_call"))
	chain.set("b", testUpstreamState(99, "eth_call"))
	resync := startGroupStream(t, chain, true, 20*time.Millisecond)
	initial := resync.next(t)
	require.Len(t, initial.Groups, 2)
	next := resync.next(t)
	assert.True(t, next.FullResponse)
	assert.True(t, proto.Equal(initial, next))
}

func TestNodeGroupStreamNetworkOnlyChange(t *testing.T) {
	chain := groupedTestChain(t)
	chain.set("a", testUpstreamState(100, "eth_call"))
	stream := startGroupStream(t, chain, false, time.Hour)
	stream.next(t)
	chain.SetState(newChainState(chains.ETHEREUM, protocol.NewBlockWithHeight(201), []string{"eth_call"}))
	chain.changed.Notify()
	response := stream.next(t)
	assert.Empty(t, response.Groups)
	assert.False(t, response.FullResponse)
	assert.True(t, slices.ContainsFunc(response.Network.ChainEvent, func(e *dshackle.ChainEvent) bool { return e.GetHead().GetHeight() == 201 }))
}
