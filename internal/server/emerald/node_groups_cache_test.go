package emerald

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Compare the incremental cache against a completely fresh group computation
// after every kind of change. Previous wire messages must remain immutable.
func TestNodeGroupDescriptionCache(t *testing.T) {
	s := groupBenchFixture(t, 4, 2, 0)
	cached := &nodeGroupStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
	now := time.Now()
	fullSeparation := false
	check := func() *dshackle.SubscribeNodeGroupStatusResponse {
		t.Helper()
		old := make(map[*dshackle.NodeGroupStatus][]byte)
		for _, g := range cached.sentGroups {
			old[g], _ = (proto.MarshalOptions{Deterministic: true}).Marshal(g)
		}
		actual := cached.groupResponse(s, now, time.Hour, fullSeparation)
		cached.nextFull = now.Add(time.Hour)
		fresh := new(nodeGroupStatusChain)
		fresh.ref = cached.ref
		fresh.announced = true
		expected := fresh.groupResponse(s, now, time.Hour, fullSeparation)
		require.Len(t, cached.sentGroups, len(expected.Groups))
		for _, g := range expected.Groups {
			require.True(t, proto.Equal(g, cached.sentGroups[g.NodeGroupId]), "group differs: %s", g.NodeGroupId)
		}
		require.True(t, proto.Equal(expected.Network, cached.sentNetwork))
		for g, before := range old {
			after, err := (proto.MarshalOptions{Deterministic: true}).Marshal(g)
			require.NoError(t, err)
			require.Equal(t, before, after, "cached wire message mutated")
		}
		return actual
	}
	publish := func(id string, change func(*protocol.UpstreamState)) {
		next := *s.states[id]
		change(&next)
		s.states[id] = next.WithNodeGroupCache()
	}
	require.Len(t, check().Groups, 2)
	require.Nil(t, check(), "unchanged snapshots must stay silent")
	next := *s.states["node-000"]
	next.HeadData = protocol.NewBlockWithHeight(1001)
	s.states["node-000"] = &next
	require.Len(t, check().Groups, 1, "head update changes only its group")
	publish("node-000", func(s *protocol.UpstreamState) {
		s.LowerBoundsInfo = s.LowerBoundsInfo.Copy()
		s.LowerBoundsInfo.AddLowerBound(protocol.NewLowerBoundData(100, 123, protocol.StateBound))
	})
	check()
	publish("node-000", func(s *protocol.UpstreamState) { s.Status = protocol.Syncing })
	check()
	publish("node-002", func(s *protocol.UpstreamState) { s.Status = protocol.Syncing })
	check()
	publish("node-000", func(s *protocol.UpstreamState) { s.Status = protocol.Available; s.UpstreamIndex = "new-index" })
	check()
	publish("node-000", func(s *protocol.UpstreamState) {
		m, err := methods.NewUpstreamMethods("eth", &config.MethodsConfig{DisableMethods: []string{"eth_call"}}, []specs.ApiConnectorType{specs.JsonRpcConnector})
		require.NoError(t, err)
		s.UpstreamMethods = m
	})
	check()
	publish("node-001", func(s *protocol.UpstreamState) {
		s.Labels = protocol.NewLabels()
		s.Labels.AddLabel("client_type", "new-client")
	})
	check()
	fullSeparation = true
	require.Len(t, check().Groups, 4)
	cached.nextFull = now
	require.True(t, check().FullResponse, "resync must include cached groups")
	s.ids = slices.DeleteFunc(s.ids, func(id string) bool { return id == "node-000" })
	delete(s.states, "node-000")
	require.Len(t, check().RemovedNodeGroupIds, 1)
	s.ids = nil
	s.states = nil
	require.Len(t, check().RemovedNodeGroupIds, 3)
	require.Empty(t, cached.groupTracker.Snapshot(s, true))
}

func TestNodeGroupCacheDoesNotRetainRetiredGroups(t *testing.T) {
	s := groupBenchFixture(t, 8, 2, 0)
	stream := &nodeGroupStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
	now := time.Now()
	for i := 0; i < 1000; i++ {
		next := *s.states["node-000"]
		next.Labels = protocol.NewLabels()
		next.Labels.AddLabel("client_type", fmt.Sprintf("changed-%d", i))
		s.states["node-000"] = next.WithNodeGroupCache()
		stream.groupResponse(s, now, time.Hour, false)
		require.Len(t, stream.sentGroups, 3)
	}
	require.Len(t, stream.groupTracker.Snapshot(s, false), 3)
}

func TestCompactGroupUpdatesAreOptIn(t *testing.T) {
	s := groupBenchFixture(t, 4, 2, 0)
	now := time.Now()
	regular := &nodeGroupStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
	compact := &nodeGroupStatusChain{ref: regular.ref, compactUpdates: true}
	require.True(t, proto.Equal(regular.groupResponse(s, now, time.Hour, false), compact.groupResponse(s, now, time.Hour, false)))
	regular.nextFull = now.Add(time.Hour)
	compact.nextFull = regular.nextFull
	next := *s.states["node-000"]
	next.HeadData = protocol.NewBlockWithHeight(1001)
	s.states["node-000"] = &next
	fullDelta := regular.groupResponse(s, now, time.Hour, false)
	small := compact.groupResponse(s, now, time.Hour, false)
	require.Len(t, small.Groups, 1)
	require.NotEmpty(t, fullDelta.Groups[0].Description)
	require.Empty(t, small.Groups[0].Description)
	require.Empty(t, small.Groups[0].UpstreamIndices)
	require.True(t, proto.Equal(fullDelta.Groups[0].Head, small.Groups[0].Head))
	require.True(t, proto.Equal(fullDelta.Network, small.Network))
	// A metadata revision includes a full description, even in compact mode.
	next.UpstreamIndex = "changed-index"
	s.states["node-000"] = next.WithNodeGroupCache()
	changed := compact.groupResponse(s, now, time.Hour, false)
	require.Len(t, changed.Groups, 1)
	require.NotEmpty(t, changed.Groups[0].Description)
	require.Equal(t, "changed-index", changed.Groups[0].UpstreamIndices["node-000"])
	compact.nextFull = now
	for _, g := range compact.groupResponse(s, now, time.Hour, false).Groups {
		require.NotEmpty(t, g.Description)
		require.NotEmpty(t, g.UpstreamIndices)
	}
}
