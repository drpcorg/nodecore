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

func newGroupTestSupervisor() *upstreams.BaseChainSupervisor {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
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
		if strings.HasPrefix(id, clientType+":") {
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
		groups := chainSupervisor.GetNodeGroupStates()
		gethId := groupIdByPrefix(groups, "geth")
		erigonId := groupIdByPrefix(groups, "erigon")
		if gethId == "" || erigonId == "" {
			return false
		}
		return groups[gethId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call")) &&
			groups[erigonId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call", "trace_block"))
	}, eventuallyWait, eventuallyTick)

	groups := chainSupervisor.GetNodeGroupStates()
	require.Len(t, groups, 2)
	gethId := groupIdByPrefix(groups, "geth")
	erigonId := groupIdByPrefix(groups, "erigon")

	assert.Equal(t, protocol.Available, groups[gethId].Status)
	assert.Equal(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, map[string]string{"client_type": "geth"})}, groups[gethId].ChainLabels)

	// heads are seeded from the state snapshots (after the recompute, so poll)
	assertEventuallyEqual(t, uint64(100), func() any { return chainSupervisor.GetNodeGroupStates()[gethId].HeadData.Head.Height })
	assertEventuallyEqual(t, uint64(90), func() any { return chainSupervisor.GetNodeGroupStates()[erigonId].HeadData.Head.Height })

	state, ok := chainSupervisor.GetNodeGroupState(gethId)
	require.True(t, ok)
	assert.Equal(t, protocol.Available, state.Status)
	_, ok = chainSupervisor.GetNodeGroupState("nethermind:00000000")
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

	assertEventuallyEqual(t, 3, func() any { return len(chainSupervisor.GetNodeGroupStates()) })

	twinGroup := func() (string, upstreams.ChainSupervisorState) {
		for id, state := range chainSupervisor.GetNodeGroupStates() {
			if strings.HasPrefix(id, "geth:") && len(state.ChainLabels) == 1 && state.ChainLabels[0].Amount == 2 {
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
	assertEventuallyEqual(t, uint64(101), func() any { return chainSupervisor.GetNodeGroupStates()[twinId].HeadData.Head.Height })

	groups := chainSupervisor.GetNodeGroupStates()
	erigonId := groupIdByPrefix(groups, "erigon")
	require.NotEmpty(t, erigonId)
	var soloGethId string
	for id := range groups {
		if strings.HasPrefix(id, "geth:") && id != twinId {
			soloGethId = id
		}
	}
	require.NotEmpty(t, soloGethId)

	// same method set, different client type: same hash suffix, different group
	assert.Equal(t, strings.SplitAfter(soloGethId, ":")[1], strings.SplitAfter(erigonId, ":")[1])
	assert.True(t, groups[soloGethId].Methods.GetSupportedMethods().Equal(mapset.NewThreadUnsafeSet[string]("eth_call")))
	assertEventuallyEqual(t, uint64(99), func() any { return chainSupervisor.GetNodeGroupStates()[soloGethId].HeadData.Head.Height })
	assertEventuallyEqual(t, uint64(98), func() any { return chainSupervisor.GetNodeGroupStates()[erigonId].HeadData.Head.Height })

	// one twin leaves: the group survives with the remaining member
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-2"))
	assertEventuallyEqual(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(1, map[string]string{"client_type": "geth"})}, func() any {
		return chainSupervisor.GetNodeGroupStates()[twinId].ChainLabels
	})
	assertEventuallyEqual(t, uint64(100), func() any { return chainSupervisor.GetNodeGroupStates()[twinId].HeadData.Head.Height })
	assertEventuallyEqual(t, 3, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
}

func TestChainSupervisorMergesSameKeyUpstreamsIntoOneGroup(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-2", protocol.Available, 101, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	assertEventuallyEqual(t, []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(2, map[string]string{"client_type": "geth"})}, func() any {
		groups := chainSupervisor.GetNodeGroupStates()
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
			groups := chainSupervisor.GetNodeGroupStates()
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
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
}

func TestChainSupervisorMovesUpstreamOnMethodsChange(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(t.Name()))

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call", "trace_block"), map[string]string{"client_type": "geth"}))
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
	oldId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(), "geth")

	// a banned/re-detected method set changes the hash and moves the upstream
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates()
		newId := groupIdByPrefix(groups, "geth")
		return len(groups) == 1 && newId != "" && newId != oldId
	}, eventuallyWait, eventuallyTick)

	// the emptied source group announced its removal
	assert.Eventually(t, func() bool { return collector.hasStatus(oldId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorMovesUpstreamOnClientTypeChange(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(t.Name()))

	// labels are detected asynchronously: the upstream starts under "unknown"
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{}))
	assert.Eventually(t, func() bool {
		return groupIdByPrefix(chainSupervisor.GetNodeGroupStates(), "unknown") != ""
	}, eventuallyWait, eventuallyTick)
	unknownId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(), "unknown")

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "erigon"}))

	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates()
		return len(groups) == 1 && groupIdByPrefix(groups, "erigon") != ""
	}, eventuallyWait, eventuallyTick)
	assert.Eventually(t, func() bool { return collector.hasStatus(unknownId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorDropsGroupOnUpstreamRemoval(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()
	collector := collectGroupEvents(chainSupervisor.SubscribeNodeGroupStates(t.Name()))

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	assertEventuallyEqual(t, 1, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
	nodeGroupId := groupIdByPrefix(chainSupervisor.GetNodeGroupStates(), "geth")

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-1"))

	assertEventuallyEqual(t, 0, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
	assert.Eventually(t, func() bool { return collector.hasStatus(nodeGroupId, protocol.Unavailable) }, eventuallyWait, eventuallyTick)
}

func TestChainSupervisorGroupStatusFollowsMemberStatuses(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-2", protocol.Syncing, 50, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))

	groupState := func() upstreams.ChainSupervisorState {
		groups := chainSupervisor.GetNodeGroupStates()
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

	assertEventuallyEqual(t, 0, func() any { return len(chainSupervisor.GetNodeGroupStates()) })
	assert.False(t, networkCollector.find(func(event *upstreams.ChainSupervisorStateWrapperEvent) bool {
		return event.NodeGroupId != ""
	}))
}

func TestChainSupervisorIgnoresHeadForUngroupedUpstream(t *testing.T) {
	chainSupervisor := newGroupTestSupervisor()

	publishHeadEvent(chainSupervisor, "up-1", protocol.Available, protocol.NewBlockWithHeight(100))
	assertEventuallyEqual(t, uint64(100), func() any { return chainSupervisor.GetChainState().HeadData.Head.Height })
	assert.Empty(t, chainSupervisor.GetNodeGroupStates())

	// the group appears with the head seeded once the state event lands
	chainSupervisor.PublishUpstreamEvent(createEventWithLabels("up-1", protocol.Available, 100, newMethodsMock("eth_call"), map[string]string{"client_type": "geth"}))
	assert.Eventually(t, func() bool {
		groups := chainSupervisor.GetNodeGroupStates()
		id := groupIdByPrefix(groups, "geth")
		return id != "" && groups[id].HeadData.Head.Height == 100
	}, eventuallyWait, eventuallyTick)
}
