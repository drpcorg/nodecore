package emerald_test

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUpstreamStreamPreservesDescriptionsAndStableIDs(t *testing.T) {
	loadMethodSpecs(t)
	chain := newUpstreamsChainSupervisor(chains.ETHEREUM)
	a, b := testUpstreamState(100, "eth_call"), testUpstreamState(200, "eth_getBalance")
	a.UpstreamIndex, b.UpstreamIndex = "00001", "00002"
	a.Labels.AddLabel("client_type", "erigon")
	b.Labels.AddLabel("client_type", "geth")
	a.Labels.AddLabel("archive", "false")
	b.Labels.AddLabel("archive", "true")
	chain.set("a", a)
	chain.set("b", b)
	stream, _ := startRecordedUpstreamStatus(t, time.Hour, chain)
	initial := stream.waitFor(t, 2)
	for _, response := range initial {
		require.True(t, response.FullResponse)
		require.Equal(t, chainRef(chains.ETHEREUM), response.ChainDescription.Chain)
		require.NotEmpty(t, response.UpstreamIndex)
		for _, event := range response.ChainDescription.ChainEvent {
			if methods := event.GetSupportedMethodsEvent(); methods != nil {
				if response.UpstreamId == "a" {
					require.Equal(t, []string{"eth_call"}, methods.Methods)
				} else {
					require.Equal(t, []string{"eth_getBalance"}, methods.Methods)
				}
			}
			if nodes := event.GetNodesEvent(); nodes != nil {
				require.Len(t, nodes.Nodes, 1)
				require.Equal(t, uint32(1), nodes.Nodes[0].Quorum)
				require.Len(t, nodes.Nodes[0].Labels, 2)
			}
		}
	}
	chain.update("a", func(s *protocol.UpstreamState) { s.HeadData = protocol.NewBlockWithHeight(101) })
	delta := stream.waitFor(t, 3)[2]
	require.Equal(t, "a", delta.UpstreamId)
	require.Equal(t, uint64(101), responseHeight(delta))
	require.Len(t, delta.ChainDescription.ChainEvent, 1)
	chain.update("a", func(s *protocol.UpstreamState) {
		s.Labels = protocol.NewLabels()
		s.Labels.AddLabel("client_type", "reth")
	})
	metadata := stream.waitFor(t, 4)[3]
	require.Equal(t, "a", metadata.UpstreamId)
	require.Zero(t, responseHeight(metadata))
	require.False(t, metadata.FullResponse)
	chain.remove("a")
	removed := stream.waitFor(t, 5)[4]
	require.Equal(t, "a", removed.UpstreamId)
	require.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, removed.ChainDescription.ChainEvent[0].GetStatus().Availability)
	chain.set("a", a)
	recovered := stream.waitFor(t, 6)
	require.Equal(t, "a", recovered[5].UpstreamId)
}

func TestUpstreamStreamResyncRepairsRemovedUpstream(t *testing.T) {
	loadMethodSpecs(t)
	chain := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chain.set("a", testUpstreamState(100, "eth_call"))
	stream, _ := startRecordedUpstreamStatus(t, 30*time.Millisecond, chain)
	stream.waitFor(t, 1)
	chain.remove("a")
	require.Eventually(t, func() bool {
		removals := 0
		for _, response := range stream.all() {
			for _, event := range response.ChainDescription.ChainEvent {
				if event.GetStatus().GetAvailability() == dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE {
					removals++
				}
			}
		}
		return removals >= 2
	}, time.Second, time.Millisecond)
}

func TestUpstreamStreamRejectsNilSupervisor(t *testing.T) {
	require.Error(t, emerald.SubscribeUpstreamStatusWithResync(nil, nil, time.Millisecond, time.Minute))
}
