package upstreams_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGroupTestSupervisor() *upstreams.GenericChainSupervisor {
	chainSupervisor := upstreams.NewGenericChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice, nil, false, nil)
	go chainSupervisor.Start()
	return chainSupervisor
}

type groupEventCollector struct {
	mu     sync.Mutex
	events []*upstreams.ChainSupervisorStateWrapperEvent
}

func collectGroupEvents(sub *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent]) *groupEventCollector {
	collector := &groupEventCollector{}
	go func() {
		for event := range sub.Events {
			collector.mu.Lock()
			collector.events = append(collector.events, event)
			collector.mu.Unlock()
		}
	}()
	return collector
}

func (c *groupEventCollector) find(match func(*upstreams.ChainSupervisorStateWrapperEvent) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if match(event) {
			return true
		}
	}
	return false
}

func (c *groupEventCollector) hasStatus(nodeGroupId string, status protocol.AvailabilityStatus) bool {
	return c.find(func(event *upstreams.ChainSupervisorStateWrapperEvent) bool {
		if event.NodeGroupId != nodeGroupId {
			return false
		}
		for _, wrapper := range event.Wrappers {
			if statusWrapper, ok := wrapper.(*upstreams.StatusWrapper); ok && statusWrapper.Status == status {
				return true
			}
		}
		return false
	})
}

func groupIdByPrefix(states map[string]upstreams.ChainSupervisorState, clientType string) string {
	for id := range states {
		if strings.HasPrefix(id, "g:"+clientType+":") {
			return id
		}
	}
	return ""
}

func TestChainSupervisorCreatesNodeGroupsPerClientTypeAndMethods(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	gethMethods := newMethodsMock("eth_call")
	erigonMethods := newMethodsMock("eth_call", "trace_block")

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-geth", protocol.Available, 100, gethMethods, map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-erigon", protocol.Available, 90, erigonMethods, map[string]string{"client_type": "erigon"}))

	// per-group state is restricted to the group members
	require.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		gethId := groupIdByPrefix(groups, "geth")
		erigonId := groupIdByPrefix(groups, "erigon")
		if gethId == "" || erigonId == "" {
			return false
		}
		return groups[gethId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call")) &&
			groups[erigonId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call", "trace_block"))
	}, eventuallyWait, eventuallyTick)

	groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
	require.Len(t, groups, 2)
	gethId := groupIdByPrefix(groups, "geth")
	erigonId := groupIdByPrefix(groups, "erigon")

	assert.Equal(t, protocol.Available, groups[gethId].Status)
	assert.Equal(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, map[string]string{"client_type": "geth"})}, groups[gethId].ChainLabels)

	// heads are seeded from the state snapshots (after the recompute, so poll)
	assertEventuallyEqual(t, uint64(100), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[gethId].HeadData.Head.Height
	})
	assertEventuallyEqual(t, uint64(90), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[erigonId].HeadData.Head.Height
	})

	state, ok := chainSupervisor.GetNodeGroupState(upstreams.SeparationGroups, gethId)
	require.True(t, ok)
	assert.Equal(t, protocol.Available, state.Status)
	_, ok = chainSupervisor.GetNodeGroupState(upstreams.SeparationGroups, "g:nethermind:00000000:00000000")
	assert.False(t, ok)
}

// four upstreams, three groups: identical twins share a group, the same
// method set under different client types stays separate
func TestChainSupervisorFourUpstreamsThreeGroups(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	twinMethods := []string{"eth_call", "eth_getBalance"}
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock(twinMethods...), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-2", protocol.Available, 101, newMethodsMock(twinMethods...), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-3", protocol.Available, 99, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-4", protocol.Available, 98, newMethodsMock("eth_call"), map[string]string{"client_type": "erigon"}))

	assertEventuallyEqual(t, 3, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })

	twinGroup := func() (string, upstreams.ChainSupervisorState) {
		for id, state := range chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups) {
			if strings.HasPrefix(id, "g:geth:") && len(state.ChainLabels) == 1 && state.ChainLabels[0].Amount == 2 {
				return id, state
			}
		}
		return "", upstreams.ChainSupervisorState{}
	}

	// the twins land in one group with both members aggregated
	require.Eventually(t, func() bool {
		_, state := twinGroup()
		return state.Methods != nil && state.Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string](twinMethods...))
	}, eventuallyWait, eventuallyTick)
	twinId, _ := twinGroup()
	assertEventuallyEqual(t, uint64(101), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[twinId].HeadData.Head.Height
	})

	groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
	erigonId := groupIdByPrefix(groups, "erigon")
	require.NotEmpty(t, erigonId)
	var soloGethId string
	for id := range groups {
		if strings.HasPrefix(id, "g:geth:") && id != twinId {
			soloGethId = id
		}
	}
	require.NotEmpty(t, soloGethId)

	// same method set, different client type: same method hash, different label
	// hash, so a different group
	soloParts, erigonParts := strings.Split(soloGethId, ":"), strings.Split(erigonId, ":")
	require.Len(t, soloParts, 4)
	require.Len(t, erigonParts, 4)
	assert.Equal(t, soloParts[3], erigonParts[3])
	assert.NotEqual(t, soloParts[2], erigonParts[2])
	assert.True(t, groups[soloGethId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call")))
	assertEventuallyEqual(t, uint64(99), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[soloGethId].HeadData.Head.Height
	})
	assertEventuallyEqual(t, uint64(98), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[erigonId].HeadData.Head.Height
	})

	// one twin leaves: the group survives with the remaining member
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-2"))
	assertEventuallyEqual(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, map[string]string{"client_type": "geth"})}, func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[twinId].ChainLabels
	})
	assertEventuallyEqual(t, uint64(100), func() any {
		return chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)[twinId].HeadData.Head.Height
	})
	assertEventuallyEqual(t, 3, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
}

func TestChainSupervisorMergesSameKeyUpstreamsIntoOneGroup(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-2", protocol.Available, 101, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	assertEventuallyEqual(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(2, map[string]string{"client_type": "geth"})}, func() any {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		if len(groups) != 1 {
			return nil
		}
		return groups[groupIdByPrefix(groups, "geth")].ChainLabels
	})
}

func TestChainSupervisorGroupHeadsFollowGroupMembers(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-geth", protocol.Available, 0, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-erigon", protocol.Available, 0, newMethodsMock("eth_call"), map[string]string{"client_type": "erigon"}))

	publishHeadEvent(chainSupervisor, "up-geth", protocol.Available, protocol.NewBlockWithHeight(100))
	publishHeadEvent(chainSupervisor, "up-erigon", protocol.Available, protocol.NewBlockWithHeight(90))

	groupHead := func(clientType string) func() any {
		return func() any {
			groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
			return groups[groupIdByPrefix(groups, clientType)].HeadData.Head.Height
		}
	}

	// network head is the max, group heads track only their members
	assertEventuallyEqual(t, uint64(100), func() any { return chainSupervisor.GetChainState().HeadData.Head.Height })
	assertEventuallyEqual(t, uint64(100), groupHead("geth"))
	assertEventuallyEqual(t, uint64(90), groupHead("erigon"))

	publishHeadEvent(chainSupervisor, "up-erigon", protocol.Available, protocol.NewBlockWithHeight(95))
	assertEventuallyEqual(t, uint64(95), groupHead("erigon"))
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().HeadData.Head.Height)

	// removal withdraws the head from both the network and the group fork choice
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-geth"))
	assertEventuallyEqual(t, uint64(95), func() any { return chainSupervisor.GetChainState().HeadData.Head.Height })
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
}

func TestChainSupervisorMovesUpstreamOnMethodsChange(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(upstreams.SeparationGroups, t.Name()))

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call", "trace_block"), map[string]string{"client_type": "geth"}))
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
	oldId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups), "geth")

	// a banned/re-detected method set changes the hash and moves the upstream
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		newId := groupIdByPrefix(groups, "geth")
		return len(groups) == 1 && newId != "" && newId != oldId
	}, eventuallyWait, eventuallyTick)

	// the emptied source group announced its removal
	assert.Eventually(t, func() bool { return collector.hasStatus(oldId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorMovesUpstreamOnClientTypeChange(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(upstreams.SeparationGroups, t.Name()))

	// labels are detected asynchronously: the upstream starts under "unknown"
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{}))
	assert.Eventually(t, func() bool {
		return groupIdByPrefix(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups), "unknown") != ""
	}, eventuallyWait, eventuallyTick)
	unknownId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups), "unknown")

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "erigon"}))

	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		return len(groups) == 1 && groupIdByPrefix(groups, "erigon") != ""
	}, eventuallyWait, eventuallyTick)
	assert.Eventually(t, func() bool { return collector.hasStatus(unknownId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorDropsGroupOnUpstreamRemoval(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(upstreams.SeparationGroups, t.Name()))

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
	nodeGroupId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups), "geth")

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-1"))

	assertEventuallyEqual(t, 0, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
	assert.Eventually(t, func() bool { return collector.hasStatus(nodeGroupId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorGroupStatusFollowsMemberStatuses(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-2", protocol.Syncing, 50, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	groupState := func() upstreams.ChainSupervisorState {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		return groups[groupIdByPrefix(groups, "geth")]
	}

	// statuses arrive already lag-downgraded (network-level validate-lag);
	// the group takes the best member status and merges available members only
	assertEventuallyEqual(t, protocol.Available, func() any { return groupState().Status })

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Syncing, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	assertEventuallyEqual(t, protocol.Syncing, func() any { return groupState().Status })
	assert.True(t, groupState().Methods.GetSupportedMethods().IsEmpty())
}

func TestChainSupervisorNetworkStreamSeesNoGroupEvents(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	networkCollector := collectGroupEvents(chainSupervisor.SubscribeState(t.Name()))

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	publishHeadEvent(chainSupervisor, "up-1", protocol.Available, protocol.NewBlockWithHeight(100))
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-1"))

	assertEventuallyEqual(t, 0, func() any { return len(chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)) })
	assert.False(t, networkCollector.find(func(event *upstreams.ChainSupervisorStateWrapperEvent) bool {
		return event.NodeGroupId != ""
	}))
}

func TestChainSupervisorIgnoresHeadForUngroupedUpstream(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	publishHeadEvent(chainSupervisor, "up-1", protocol.Available, protocol.NewBlockWithHeight(100))
	assertEventuallyEqual(t, uint64(100), func() any { return chainSupervisor.GetChainState().HeadData.Head.Height })
	assert.Empty(t, chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups))

	// the group appears with the head seeded once the state event lands
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates(upstreams.SeparationGroups)
		id := groupIdByPrefix(groups, "geth")
		return id != "" && groups[id].HeadData.Head.Height == 100
	}, eventuallyWait, eventuallyTick)
}
