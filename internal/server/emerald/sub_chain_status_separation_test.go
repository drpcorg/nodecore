package emerald_test

import (
	"context"
	"strings"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const gethGroupId = "geth:3d801bd0"

func startSeparationStream(
	t *testing.T,
	chainSupervisor *fakeChainSupervisor,
	separation bool,
) (*subscribeChainStatusStream, chan error) {
	t.Helper()
	return startSeparationStreamWithResync(t, chainSupervisor, separation, time.Minute)
}

func startSeparationStreamWithResync(
	t *testing.T,
	chainSupervisor *fakeChainSupervisor,
	separation bool,
	resyncInterval time.Duration,
) (*subscribeChainStatusStream, chan error) {
	t.Helper()

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatusWithResync(upstreamSupervisor, stream, resyncInterval, separation)
	}()

	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	return stream, done
}

func (s *subscribeChainStatusStream) groupResponses(nodeGroupId string) []*dshackle.SubscribeChainStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*dshackle.SubscribeChainStatusResponse, 0)
	for _, response := range s.responses {
		if response.GetChainDescription().GetNodeGroupId() == nodeGroupId {
			result = append(result, response)
		}
	}
	return result
}

func waitForResponses(t *testing.T, stream *subscribeChainStatusStream, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return stream.Count() >= count
	}, time.Second, 10*time.Millisecond)
}

func TestSubscribeChainStatus_CapsOnlyDeltaSendsNothing(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 2)
	count := stream.Count()

	// caps have no wire event: a caps-only delta must not reach the client
	chainSupervisor.PublishStateEvent(upstreams.NewCapsWrapper(mapset.NewThreadUnsafeSet(protocol.WsCap)))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewCapsWrapper(mapset.NewThreadUnsafeSet(protocol.WsCap)))

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, count, stream.Count())

	// in a mixed delta the caps wrapper is dropped, the rest goes through
	chainSupervisor.PublishGroupStateEvent(gethGroupId,
		upstreams.NewCapsWrapper(mapset.NewThreadUnsafeSet(protocol.WsCap)),
		upstreams.NewStatusWrapper(protocol.Syncing),
	)
	waitForResponses(t, stream, count+1)
	responses := stream.groupResponses(gethGroupId)
	delta := responses[len(responses)-1]
	require.Len(t, delta.GetChainDescription().GetChainEvent(), 1)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, delta.GetChainDescription().GetChainEvent()[0].GetStatus().GetAvailability())
}

func TestSubscribeChainStatusSeparation_OffSendsNoGroupEvents(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, false)
	waitForResponses(t, stream, 1)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, stream.Count(), "separation off must not produce group responses")
	assert.Empty(t, stream.ResponseAt(0).GetChainDescription().GetNodeGroupId())
}

func TestSubscribeChainStatusSeparation_SendsGroupFullsAfterNetworkFull(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState("erigon:11111111", newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(120), []string{"eth_call", "trace_block"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 3)

	network := stream.ResponseAt(0)
	assert.Empty(t, network.GetChainDescription().GetNodeGroupId())
	assert.True(t, network.FullResponse)
	assert.NotNil(t, network.BuildInfo)

	for _, nodeGroupId := range []string{gethGroupId, "erigon:11111111"} {
		responses := stream.groupResponses(nodeGroupId)
		require.Len(t, responses, 1, "expected exactly one full for %s", nodeGroupId)
		assert.True(t, responses[0].FullResponse)
		assert.Nil(t, responses[0].BuildInfo, "group fulls must not carry BuildInfo")
		assert.Len(t, responses[0].GetChainDescription().GetChainEvent(), 7)
	}
}

func TestSubscribeChainStatusSeparation_GroupDeltaAfterFull(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 2)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Syncing))
	waitForResponses(t, stream, 3)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	delta := responses[1]
	assert.False(t, delta.FullResponse)
	require.Len(t, delta.GetChainDescription().GetChainEvent(), 1)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, delta.GetChainDescription().GetChainEvent()[0].GetStatus().GetAvailability())
}

func TestSubscribeChainStatusSeparation_NewGroupAnnouncedWithFull(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 1)

	// a group formed after subscription: its first delta triggers a full instead
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	waitForResponses(t, stream, 2)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 1)
	assert.True(t, responses[0].FullResponse)
	assert.Len(t, responses[0].GetChainDescription().GetChainEvent(), 7)

	// the next delta flows through as-is
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Syncing))
	waitForResponses(t, stream, 3)
	responses = stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	assert.False(t, responses[1].FullResponse)
}

func TestSubscribeChainStatusSeparation_HeadGatesGroupFull(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	// group exists but has no head yet
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 1)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, stream.groupResponses(gethGroupId), "headless group must not be announced")

	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewHeadWrapper(head, "up-1"))
	waitForResponses(t, stream, 2)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 1)
	assert.True(t, responses[0].FullResponse)
}

func TestSubscribeChainStatusSeparation_HoldsGroupEventsUntilNetworkFull(t *testing.T) {
	// no network head yet: nothing at all may be sent
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))
	head := protocol.NewBlockWithHeight(100)
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 0, stream.Count(), "group events must wait for the network full")

	// the network head arrives: network full first, then the group full
	chainSupervisor.SetState(newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishStateEvent(upstreams.NewHeadWrapper(head, "up-1"))
	waitForResponses(t, stream, 2)

	assert.Empty(t, stream.ResponseAt(0).GetChainDescription().GetNodeGroupId())
	assert.True(t, stream.ResponseAt(0).FullResponse)
	groupResponses := stream.groupResponses(gethGroupId)
	require.Len(t, groupResponses, 1)
	assert.True(t, groupResponses[0].FullResponse)
}

func TestSubscribeChainStatusSeparation_ForwardsGroupRemoval(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStream(t, chainSupervisor, true)
	waitForResponses(t, stream, 2)

	// the supervisor publishes Unavailable for an emptied group and drops it
	chainSupervisor.DeleteGroupState(gethGroupId)
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Unavailable))
	waitForResponses(t, stream, 3)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	removal := responses[1]
	assert.False(t, removal.FullResponse)
	require.Len(t, removal.GetChainDescription().GetChainEvent(), 1)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, removal.GetChainDescription().GetChainEvent()[0].GetStatus().GetAvailability())
}

func TestSubscribeChainStatusSeparation_ResyncSnapshotsGroups(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStreamWithResync(t, chainSupervisor, true, testResyncInterval)
	waitForResponses(t, stream, 2)

	// resync: a snapshot without a head for the announced group
	require.Eventually(t, func() bool {
		return len(stream.groupResponses(gethGroupId)) >= 2
	}, time.Second, 10*time.Millisecond)

	snapshot := stream.groupResponses(gethGroupId)[1]
	assert.False(t, snapshot.FullResponse)
	events := snapshot.GetChainDescription().GetChainEvent()
	require.NotEmpty(t, events)
	for _, event := range events {
		assert.Nil(t, event.GetHead(), "group resync must not carry a head")
	}
}

func TestSubscribeChainStatusSeparation_ResyncAnnouncesSilentGroupAndPurgesDropped(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startSeparationStreamWithResync(t, chainSupervisor, true, testResyncInterval)
	waitForResponses(t, stream, 1)

	// a group whose announce delta was lost is announced by the resync
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	require.Eventually(t, func() bool {
		responses := stream.groupResponses(gethGroupId)
		return len(responses) >= 1 && responses[0].FullResponse
	}, time.Second, 10*time.Millisecond)

	// dropped group: the stale announce state is purged, so a re-formed group
	// with the same id is re-announced with a fresh full
	chainSupervisor.DeleteGroupState(gethGroupId)
	// two network snapshots guarantee a resync tick fully ran after the delete
	networkBefore := len(stream.groupResponses(""))
	require.Eventually(t, func() bool {
		return len(stream.groupResponses("")) >= networkBefore+2
	}, time.Second, 10*time.Millisecond)

	groupBefore := len(stream.groupResponses(gethGroupId))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	require.Eventually(t, func() bool {
		responses := stream.groupResponses(gethGroupId)
		return len(responses) > groupBefore && responses[len(responses)-1].FullResponse
	}, time.Second, 10*time.Millisecond)
}

func realUpstreamState(clientType string, height uint64, methods ...string) *protocol.UpstreamState {
	state := protocol.DefaultUpstreamState(newMethodsMockWithSupported(methods...), mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	state.HeadData = protocol.Block{Height: height}
	if clientType != "" {
		state.Labels.AddLabel("client_type", clientType)
	}
	return &state
}

// the whole nodecore-side flow with a real chain supervisor: upstream events
// in, tagged stream frames out
func TestSubscribeChainStatusSeparation_RealChainSupervisorFlow(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream, true)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})

	groupFulls := func(clientType string) []string {
		stream.mu.RLock()
		defer stream.mu.RUnlock()
		ids := make([]string, 0)
		for _, response := range stream.responses {
			nodeGroupId := response.GetChainDescription().GetNodeGroupId()
			if response.FullResponse && strings.HasPrefix(nodeGroupId, clientType+":") {
				ids = append(ids, nodeGroupId)
			}
		}
		return ids
	}
	hasUnavailableDelta := func(nodeGroupId string) bool {
		stream.mu.RLock()
		defer stream.mu.RUnlock()
		for _, response := range stream.responses {
			if response.GetChainDescription().GetNodeGroupId() != nodeGroupId || response.FullResponse {
				continue
			}
			for _, event := range response.GetChainDescription().GetChainEvent() {
				if event.GetStatus().GetAvailability() == dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE {
					return true
				}
			}
		}
		return false
	}

	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "up-geth",
		EventType: &protocol.StateUpstreamEvent{State: realUpstreamState("geth", 100, "eth_call")},
	})
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "up-geth",
		EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: protocol.NewBlockWithHeight(100)},
	})

	// network full first, then the group full tagged with the derived id
	waitForResponses(t, stream, 2)
	assert.True(t, stream.ResponseAt(0).FullResponse)
	assert.Empty(t, stream.ResponseAt(0).GetChainDescription().GetNodeGroupId())
	require.Eventually(t, func() bool { return len(groupFulls("geth")) == 1 }, time.Second, 10*time.Millisecond)
	firstGroupId := groupFulls("geth")[0]

	// a changed method set moves the upstream: the old group goes unavailable,
	// the new one is announced with a full under a different id
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "up-geth",
		EventType: &protocol.StateUpstreamEvent{State: realUpstreamState("geth", 100, "eth_call", "eth_getBalance")},
	})
	require.Eventually(t, func() bool { return len(groupFulls("geth")) == 2 }, time.Second, 10*time.Millisecond)
	secondGroupId := groupFulls("geth")[1]
	assert.NotEqual(t, firstGroupId, secondGroupId)
	assert.Eventually(t, func() bool { return hasUnavailableDelta(firstGroupId) }, time.Second, 10*time.Millisecond)

	// removing the last member announces the group's unavailability
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "up-geth", EventType: &protocol.RemoveUpstreamEvent{}})
	assert.Eventually(t, func() bool { return hasUnavailableDelta(secondGroupId) }, time.Second, 10*time.Millisecond)
}
