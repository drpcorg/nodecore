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
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const gethGroupId = "geth:3d801bd0"

func startNodeGroupStream(
	t *testing.T,
	chainSupervisor upstreams.ChainSupervisor,
) (*subscribeChainStatusStream, chan error) {
	t.Helper()
	return startNodeGroupStreamWithResync(t, chainSupervisor, time.Minute)
}

func startNodeGroupStreamWithResync(
	t *testing.T,
	chainSupervisor upstreams.ChainSupervisor,
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
		done <- emerald.SubscribeNodeGroupStatusWithResync(upstreamSupervisor, stream, resyncInterval)
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

func TestSubscribeNodeGroupStatus_NilSupervisorReturnsError(t *testing.T) {
	err := emerald.SubscribeNodeGroupStatus(nil, newSubscribeChainStatusStream())

	require.Error(t, err)
	assert.Equal(t, "upstream supervisor cannot be nil", err.Error())
}

func TestSubscribeChainStatus_CarriesNoGroupEvents(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	waitForResponses(t, stream, 1)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, stream.Count(), "the merged stream must not carry group responses")
	assert.Empty(t, stream.ResponseAt(0).GetChainDescription().GetNodeGroupId())
}

func TestSubscribeNodeGroupStatus_AnnouncesLiveGroupsOnSubscribe(t *testing.T) {
	// no network head at all: the group stream must not depend on the merged view
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState("erigon:11111111", newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(120), []string{"eth_call", "trace_block"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)
	waitForResponses(t, stream, 2)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, stream.Count(), "one full per live group, nothing else")

	for _, nodeGroupId := range []string{gethGroupId, "erigon:11111111"} {
		responses := stream.groupResponses(nodeGroupId)
		require.Len(t, responses, 1, "expected exactly one full for %s", nodeGroupId)
		assert.True(t, responses[0].FullResponse)
		require.NotNil(t, responses[0].BuildInfo, "group fulls carry BuildInfo - the consumer's min-version check reads it")
		assert.True(t, strings.HasPrefix(responses[0].BuildInfo.Version, "nodecore/"))
		assert.Len(t, responses[0].GetChainDescription().GetChainEvent(), 7)
	}
}

func TestSubscribeNodeGroupStatus_GroupDeltaAfterFull(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)
	waitForResponses(t, stream, 1)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Syncing))
	waitForResponses(t, stream, 2)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	delta := responses[1]
	assert.False(t, delta.FullResponse)
	require.Len(t, delta.GetChainDescription().GetChainEvent(), 1)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, delta.GetChainDescription().GetChainEvent()[0].GetStatus().GetAvailability())
}

func TestSubscribeNodeGroupStatus_NewGroupAnnouncedWithFull(t *testing.T) {
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)

	// a group formed after subscription: its first delta triggers a full instead
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	waitForResponses(t, stream, 1)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 1)
	assert.True(t, responses[0].FullResponse)
	assert.Len(t, responses[0].GetChainDescription().GetChainEvent(), 7)

	// the next delta flows through as-is
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Syncing))
	waitForResponses(t, stream, 2)
	responses = stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	assert.False(t, responses[1].FullResponse)
}

func TestSubscribeNodeGroupStatus_HeadGatesGroupFull(t *testing.T) {
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))
	// group exists but has no head yet
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)

	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, stream.Count(), "headless group must not be announced")

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewHeadWrapper(head, "up-1"))
	waitForResponses(t, stream, 1)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 1)
	assert.True(t, responses[0].FullResponse)
}

func TestSubscribeNodeGroupStatus_DoesNotAnnounceUnavailableGroup(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	unavailable := newChainState(chains.ARBITRUM, head, []string{"eth_call"})
	unavailable.Status = protocol.Unavailable
	chainSupervisor.SetGroupState(gethGroupId, unavailable)

	stream, _ := startNodeGroupStream(t, chainSupervisor)

	// neither the initial sync nor deltas may announce a dead group
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Unavailable))
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, stream.Count())

	// it is introduced once it recovers
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	waitForResponses(t, stream, 1)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 1)
	assert.True(t, responses[0].FullResponse)
}

func TestSubscribeNodeGroupStatus_ForwardsGroupRemovalAndReannounces(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)
	waitForResponses(t, stream, 1)

	// the supervisor publishes Unavailable for an emptied group and drops it
	chainSupervisor.DeleteGroupState(gethGroupId)
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Unavailable))
	waitForResponses(t, stream, 2)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 2)
	removal := responses[1]
	assert.False(t, removal.FullResponse)
	require.Len(t, removal.GetChainDescription().GetChainEvent(), 1)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, removal.GetChainDescription().GetChainEvent()[0].GetStatus().GetAvailability())

	// the consumer dropped the group on that signal: a re-formed group under
	// the same id must be re-announced with a fresh full
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	waitForResponses(t, stream, 3)

	responses = stream.groupResponses(gethGroupId)
	require.Len(t, responses, 3)
	assert.True(t, responses[2].FullResponse)
}

func TestSubscribeNodeGroupStatus_ReannouncesGroupRecoveredFromUnavailable(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)
	waitForResponses(t, stream, 1)

	// all members dip: the group stays live but its status goes Unavailable
	unavailable := newChainState(chains.ARBITRUM, head, []string{"eth_call"})
	unavailable.Status = protocol.Unavailable
	chainSupervisor.SetGroupState(gethGroupId, unavailable)
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Unavailable))
	waitForResponses(t, stream, 2)

	// recovery must be re-announced with a full, not a plain delta
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishGroupStateEvent(gethGroupId, upstreams.NewStatusWrapper(protocol.Available))
	waitForResponses(t, stream, 3)

	responses := stream.groupResponses(gethGroupId)
	require.Len(t, responses, 3)
	assert.False(t, responses[1].FullResponse)
	assert.True(t, responses[2].FullResponse)
}

func TestSubscribeNodeGroupStatus_CapsOnlyDeltaSendsNothing(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStream(t, chainSupervisor)
	waitForResponses(t, stream, 1)
	count := stream.Count()

	// caps have no wire event: a caps-only delta must not reach the client
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

func TestSubscribeNodeGroupStatus_ResyncSnapshotsGroups(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStreamWithResync(t, chainSupervisor, testResyncInterval)
	waitForResponses(t, stream, 1)

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

func TestSubscribeNodeGroupStatus_ResyncTombstonesDroppedGroupAndReannounces(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	stream, _ := startNodeGroupStreamWithResync(t, chainSupervisor, testResyncInterval)

	// a group whose announce delta was lost is announced by the resync
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	require.Eventually(t, func() bool {
		responses := stream.groupResponses(gethGroupId)
		return len(responses) >= 1 && responses[0].FullResponse
	}, time.Second, 10*time.Millisecond)

	// dropped group whose removal delta was lost: the resync tombstones it
	chainSupervisor.DeleteGroupState(gethGroupId)
	tombstoneIdx := -1
	require.Eventually(t, func() bool {
		responses := stream.groupResponses(gethGroupId)
		for i, response := range responses {
			events := response.GetChainDescription().GetChainEvent()
			if !response.FullResponse && len(events) == 1 &&
				events[0].GetStatus().GetAvailability() == dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE {
				tombstoneIdx = i
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	// ...and the re-formed group is re-announced with a fresh full
	chainSupervisor.SetGroupState(gethGroupId, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	require.Eventually(t, func() bool {
		responses := stream.groupResponses(gethGroupId)
		for _, response := range responses[tombstoneIdx+1:] {
			if response.FullResponse {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
}

func gethStateEvent(id string, height uint64, methods ...string) protocol.UpstreamEvent {
	return test_utils.CreateEventWithLabels(id, protocol.Available, protocol.NewBlockWithHeight(height),
		newMethodsMockWithSupported(methods...), map[string]string{"client_type": "geth"})
}

// the whole nodecore-side flow with a real chain supervisor: upstream events
// in, tagged group frames out
func TestSubscribeNodeGroupStatus_RealChainSupervisorFlow(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()

	stream, _ := startNodeGroupStream(t, chainSupervisor)

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

	chainSupervisor.PublishUpstreamEvent(gethStateEvent("up-geth", 100, "eth_call"))

	// the group full is announced from the group's own seeded head - no merged
	// head, no network full involved
	require.Eventually(t, func() bool { return len(groupFulls("geth")) == 1 }, time.Second, 10*time.Millisecond)
	firstGroupId := groupFulls("geth")[0]

	// a changed method set moves the upstream: the old group goes unavailable,
	// the new one is announced with a full under a different id
	chainSupervisor.PublishUpstreamEvent(gethStateEvent("up-geth", 100, "eth_call", "eth_getBalance"))
	require.Eventually(t, func() bool { return len(groupFulls("geth")) == 2 }, time.Second, 10*time.Millisecond)
	secondGroupId := groupFulls("geth")[1]
	assert.NotEqual(t, firstGroupId, secondGroupId)
	assert.Eventually(t, func() bool { return hasUnavailableDelta(firstGroupId) }, time.Second, 10*time.Millisecond)

	// removing the last member announces the group's unavailability
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-geth"))
	assert.Eventually(t, func() bool { return hasUnavailableDelta(secondGroupId) }, time.Second, 10*time.Millisecond)
}
