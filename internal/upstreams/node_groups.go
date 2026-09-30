package upstreams

import (
	"slices"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

type NodeGroupSnapshot struct {
	Id      string
	State   ChainSupervisorState
	Indices map[string]string
}

// NodeGroups applies the existing merge pipeline independently to each group.
// These heads are observability only: upstream statuses already include the
// authoritative network lag validation. Rebuilding the small snapshot makes
// removals, reorgs and membership changes explicit, with no second event queue.
func NodeGroups(supervisor ChainSupervisor, full bool) map[string]*NodeGroupSnapshot {
	groups := make(map[string]*NodeGroupSnapshot)
	members := make(map[string][]*protocol.UpstreamState)
	choices := make(map[string]*fork_choice.HeightForkChoice)
	ids := supervisor.GetUpstreamIds()
	slices.Sort(ids)
	for _, id := range ids {
		state := supervisor.GetUpstreamState(id)
		if state == nil {
			continue
		}
		key := protocol.NodeGroupID(id, state, full)
		group := groups[key]
		if group == nil {
			group = &NodeGroupSnapshot{Id: key, State: ChainSupervisorState{Status: protocol.Unavailable}, Indices: make(map[string]string)}
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
		available := members[key]
		state := &group.State
		state.Methods = processUpstreamMethods(available)
		state.Blocks = processUpstreamBlocks(available)
		state.LowerBounds = processLowerBounds(available)
		state.ChainLabels = processLabels(available)
		state.Caps = processCaps(available)
		state.SubMethods = ProcessSubMethods(subMethods, state.Methods, state.Caps)
	}
	return groups
}
