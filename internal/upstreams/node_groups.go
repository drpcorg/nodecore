package upstreams

import (
	"slices"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

type NodeGroupSnapshot struct {
	State                ChainSupervisorState
	Indices              map[string]string
	DescriptionUnchanged bool
}

// NodeGroupTracker belongs to one status stream. Descriptions are immutable and
// reused across head-only snapshots; membership/state changes rebuild the owning
// group. It does not change network validation or retain removed members.
type NodeGroupTracker struct {
	states map[string]*protocol.UpstreamState
	groups map[string]*NodeGroupSnapshot
	full   bool
}

func (tracker *NodeGroupTracker) Snapshot(supervisor ChainSupervisor, full bool) map[string]*NodeGroupSnapshot {
	groups := make(map[string]*NodeGroupSnapshot)
	states := make(map[string]*protocol.UpstreamState)
	dirty := make(map[string]bool)
	members := make(map[string][]*protocol.UpstreamState)
	choices := make(map[string]fork_choice.ForkChoice)
	ids := supervisor.GetUpstreamIds()
	slices.Sort(ids)
	for _, id := range ids {
		state := supervisor.GetUpstreamState(id)
		if state == nil {
			continue
		}
		states[id] = state
		key := protocol.NodeGroupID(id, state, full)
		if tracker.full != full || !state.SameGroupDescription(tracker.states[id]) {
			dirty[key] = true
		}
		group := groups[key]
		if group == nil {
			group = &NodeGroupSnapshot{State: ChainSupervisorState{Status: protocol.Unavailable}, Indices: make(map[string]string)}
			groups[key] = group
			choices[key] = fork_choice.NewHeightForkChoice()
		}
		group.Indices[id] = state.UpstreamIndex
		group.State.Status = min(group.State.Status, state.Status)
		if state.Status == protocol.Available {
			members[key] = append(members[key], state)
		}
		_, head := choices[key].Choose(id, &protocol.HeadUpstreamEvent{Status: state.Status, Head: state.HeadData})
		group.State.HeadData = NewChainHeadData(head, id)
	}
	subMethods := specs.GetSubMethods(chains.GetMethodSpecNameByChain(supervisor.GetChain()))
	for key, group := range groups {
		if previous := tracker.groups[key]; previous != nil && !dirty[key] && len(previous.Indices) == len(group.Indices) {
			head, status := group.State.HeadData, group.State.Status
			group.State = previous.State
			group.State.HeadData, group.State.Status = head, status
			group.DescriptionUnchanged = true
			continue
		}
		available := members[key]
		state := &group.State
		state.Methods = processUpstreamMethods(available)
		state.Blocks = processUpstreamBlocks(available)
		state.LowerBounds = processLowerBounds(available)
		state.ChainLabels = processLabels(available)
		state.Caps = processCaps(available)
		state.SubMethods = ProcessSubMethods(subMethods, state.Methods, state.Caps)
	}
	tracker.states, tracker.groups, tracker.full = states, groups, full
	return groups
}
