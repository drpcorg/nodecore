package upstreams

import (
	"maps"
	"slices"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// one block moves every group of a chain at once, so the network default is too small
const nodeGroupSubscriptionSize = 1000

// nodeGroups partitions the upstreams of a chain into the node groups of one
// separation level. It is mutated only from the supervisor event loop; groups
// and each group's state are also read from other goroutines.
type nodeGroups struct {
	level           SeparationLevel
	groups          *utils.CMap[string, *groupState]
	membership      map[string]string // upstream id -> group id
	subs            *utils.SubscriptionManager[*ChainSupervisorStateWrapperEvent]
	newForkChoice   func() choice.ForkChoice
	subChainMethods mapset.Set[string]
}

type groupState struct {
	id      string
	fc      choice.ForkChoice
	members map[string]*protocol.UpstreamState
	state   *utils.Atomic[ChainSupervisorState]
}

func newNodeGroups(level SeparationLevel, newForkChoice func() choice.ForkChoice, subChainMethods mapset.Set[string]) *nodeGroups {
	return &nodeGroups{
		level:           level,
		groups:          utils.NewCMap[string, *groupState](),
		membership:      make(map[string]string),
		subs:            utils.NewSubscriptionManager[*ChainSupervisorStateWrapperEvent]("node_group_events_" + level.String()),
		newForkChoice:   newForkChoice,
		subChainMethods: subChainMethods,
	}
}

func newGroupState(id string, fc choice.ForkChoice) *groupState {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(initialChainSupervisorState())
	return &groupState{
		id:      id,
		fc:      fc,
		members: make(map[string]*protocol.UpstreamState),
		state:   state,
	}
}

func (b *GenericChainSupervisor) SubscribeNodeGroupStates(level SeparationLevel, name string) *utils.Subscription[*ChainSupervisorStateWrapperEvent] {
	return b.nodeGroupsAt(level).subs.SubscribeWithSize(name, nodeGroupSubscriptionSize)
}

func (b *GenericChainSupervisor) GetNodeGroupStates(level SeparationLevel) map[string]ChainSupervisorState {
	states := make(map[string]ChainSupervisorState)
	b.nodeGroupsAt(level).groups.Range(func(id string, g *groupState) bool {
		states[id] = g.state.Load()
		return true
	})
	return states
}

func (b *GenericChainSupervisor) GetNodeGroupState(level SeparationLevel, id string) (ChainSupervisorState, bool) {
	g, ok := b.nodeGroupsAt(level).groups.Load(id)
	if !ok {
		return ChainSupervisorState{}, false
	}
	return g.state.Load(), true
}

// nodeGroupsAt answers a level outside the enum with no groups and no events.
func (b *GenericChainSupervisor) nodeGroupsAt(level SeparationLevel) *nodeGroups {
	if groups, ok := b.nodeGroups[level]; ok {
		return groups
	}
	return newNodeGroups(level, nil, nil)
}

func (b *GenericChainSupervisor) updateNodeGroups(upstreamId string, state *protocol.UpstreamState) {
	for _, groups := range b.nodeGroups {
		groups.update(upstreamId, state)
	}
}

func (b *GenericChainSupervisor) updateNodeGroupHeads(upstreamId string, headEvent *protocol.HeadUpstreamEvent) {
	for _, groups := range b.nodeGroups {
		groups.updateHead(upstreamId, headEvent)
	}
}

func (b *GenericChainSupervisor) removeFromNodeGroups(upstreamId string) {
	for _, groups := range b.nodeGroups {
		groups.leave(upstreamId)
	}
	forgetNodeGroupIds(upstreamId)
}

// update files the upstream under the group its state derives, moving it
// when the id changed.
func (p *nodeGroups) update(upstreamId string, state *protocol.UpstreamState) {
	id := CachedNodeGroupId(p.level, upstreamId, state)
	if current, ok := p.membership[upstreamId]; ok {
		if current == id {
			p.updateMember(p.group(id), upstreamId, state)
			return
		}
		p.leave(upstreamId)
	}
	p.join(id, upstreamId, state)
}

func (p *nodeGroups) updateMember(g *groupState, upstreamId string, state *protocol.UpstreamState) {
	changed := changedFacets(g.members[upstreamId], state)
	g.members[upstreamId] = state
	if changed != 0 {
		p.recompute(g, changed)
	}
}

func (p *nodeGroups) join(id, upstreamId string, state *protocol.UpstreamState) {
	g, _ := p.groups.LoadOrStoreLazy(id, func() *groupState { return newGroupState(id, p.newForkChoice()) })
	g.members[upstreamId] = state
	p.membership[upstreamId] = id
	p.recompute(g, allFacets)
	// Seed the group head from the snapshot so a join doesn't wait for the next
	// block. After the recompute: the stream announces a group on its head and
	// must never see the placeholder state.
	p.updateGroupHead(g, upstreamId, &protocol.HeadUpstreamEvent{Status: state.Status, Head: state.HeadData})
}

func (p *nodeGroups) leave(upstreamId string) {
	id, ok := p.membership[upstreamId]
	if !ok {
		return
	}
	g := p.group(id)
	delete(p.membership, upstreamId)
	delete(g.members, upstreamId)
	p.updateGroupHead(g, upstreamId, &protocol.HeadUpstreamEvent{Status: protocol.Unavailable})
	// an emptied group recomputes to Unavailable, the stream's removal signal
	p.recompute(g, allFacets)
	if len(g.members) == 0 {
		p.groups.Delete(id)
	}
}

// updateHead ignores upstreams without a state event yet: join seeds their head.
func (p *nodeGroups) updateHead(upstreamId string, headEvent *protocol.HeadUpstreamEvent) {
	if id, ok := p.membership[upstreamId]; ok {
		p.updateGroupHead(p.group(id), upstreamId, headEvent)
	}
}

func (p *nodeGroups) group(id string) *groupState {
	g, _ := p.groups.Load(id)
	return g
}

func (p *nodeGroups) recompute(g *groupState, changed stateFacets) {
	prev := g.state.Load()
	next := recomputeFacets(prev, slices.Collect(maps.Values(g.members)), changed, p.subChainMethods)
	g.state.Store(next)
	if wrappers := prev.Compare(next); len(wrappers) > 0 {
		p.subs.Publish(&ChainSupervisorStateWrapperEvent{Wrappers: wrappers, NodeGroupId: g.id})
	}
}

func (p *nodeGroups) updateGroupHead(g *groupState, upstreamId string, headEvent *protocol.HeadUpstreamEvent) {
	chooseHead(g.fc, g.state, p.subs, g.id, upstreamId, headEvent)
}
