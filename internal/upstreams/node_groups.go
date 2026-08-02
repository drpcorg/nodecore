package upstreams

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

const unknownClientType = "unknown"

// GroupKey identifies a node group within one chain: same client type and the
// same supported call-method set.
type GroupKey struct {
	ClientType  string
	MethodsHash string
}

func (k GroupKey) Id() string {
	return k.ClientType + ":" + k.MethodsHash
}

// groupKeyOf derives the owning group from an upstream state snapshot.
// SubMethods and Caps deliberately don't participate. The client_type label is
// detected asynchronously, so an upstream may start under "unknown" and move
// once the label lands.
func groupKeyOf(state *protocol.UpstreamState) GroupKey {
	return GroupKey{ClientType: clientTypeOf(state), MethodsHash: methodsHash(state.UpstreamMethods)}
}

func clientTypeOf(state *protocol.UpstreamState) string {
	clientType := ""
	if state.Labels != nil {
		clientType, _ = state.Labels.GetLabel("client_type")
	}
	if clientType == "" {
		clientType = unknownClientType
	}
	return clientType
}

// groupMembership caches the derived key next to its inputs: state events fire
// on every block/bound advance, and UpstreamMethods is copy-on-write (replaced
// only on ban/unban), so pointer identity spares the per-event rehash.
type groupMembership struct {
	key        GroupKey
	methods    methods.Methods
	clientType string
}

func methodsHash(m methods.Methods) string {
	var names []string
	if m != nil {
		names = m.GetSupportedMethods().ToSlice()
	}
	slices.Sort(names)
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return hex.EncodeToString(sum[:4])
}

// groupState is owned by the supervisor event loop; only the Atomic state is
// read from other goroutines.
type groupState struct {
	id      string
	fc      choice.ForkChoice
	members mapset.Set[string]
	state   *utils.Atomic[ChainSupervisorState]
}

func newGroupState(id string) *groupState {
	state := utils.NewAtomic[ChainSupervisorState]()
	state.Store(initialChainSupervisorState())
	return &groupState{
		id:      id,
		fc:      choice.NewHeightForkChoice(),
		members: mapset.NewThreadUnsafeSet[string](),
		state:   state,
	}
}

func (b *BaseChainSupervisor) SubscribeNodeGroupStates(name string) *utils.Subscription[*ChainSupervisorStateWrapperEvent] {
	return b.subGroupStateManager.Subscribe(name)
}

func (b *BaseChainSupervisor) GetNodeGroupStates() map[string]ChainSupervisorState {
	states := make(map[string]ChainSupervisorState)
	b.groups.Range(func(id string, g *groupState) bool {
		states[id] = g.state.Load()
		return true
	})
	return states
}

func (b *BaseChainSupervisor) GetNodeGroupState(id string) (ChainSupervisorState, bool) {
	g, ok := b.groups.Load(id)
	if !ok {
		return ChainSupervisorState{}, false
	}
	return g.state.Load(), true
}

func (b *BaseChainSupervisor) assignGroup(id string, state *protocol.UpstreamState) {
	if state == nil {
		return
	}
	current, had := b.upstreamGroup[id]
	clientType := clientTypeOf(state)
	newKey := current.key
	if !had || current.methods != state.UpstreamMethods || current.clientType != clientType {
		newKey = GroupKey{ClientType: clientType, MethodsHash: methodsHash(state.UpstreamMethods)}
	}
	b.upstreamGroup[id] = groupMembership{key: newKey, methods: state.UpstreamMethods, clientType: clientType}
	if had && current.key == newKey {
		// same group, a member's state changed
		if g, ok := b.groups.Load(newKey.Id()); ok {
			b.recomputeGroup(g)
		}
		return
	}
	if had {
		b.leaveGroup(id, current.key)
	}
	nodeGroupId := newKey.Id()
	g, _ := b.groups.LoadOrStoreLazy(nodeGroupId, func() *groupState { return newGroupState(nodeGroupId) })
	g.members.Add(id)
	b.recomputeGroup(g)
	// Seed the group fork choice from the stored snapshot so a join doesn't
	// wait for the next block; fc.Choose ignores non-available/empty heads.
	// Seeding after the recompute keeps the announce order sane: the stream
	// announces on the head wrapper and must never see the placeholder state.
	b.updateGroupHeadIn(g, id, &protocol.HeadUpstreamEvent{Status: state.Status, Head: state.HeadData})
}

func (b *BaseChainSupervisor) removeFromGroup(id string) {
	if membership, ok := b.upstreamGroup[id]; ok {
		delete(b.upstreamGroup, id)
		b.leaveGroup(id, membership.key)
	}
}

func (b *BaseChainSupervisor) leaveGroup(id string, key GroupKey) {
	g, ok := b.groups.Load(key.Id())
	if !ok {
		return
	}
	g.members.Remove(id)
	// withdraw the member's head from the group fork choice (may lower the head)
	b.updateGroupHeadIn(g, id, &protocol.HeadUpstreamEvent{Status: protocol.Unavailable})
	// an emptied group recomputes to Status=Unavailable - that is the removal
	// signal for stream consumers; only then is the group dropped
	b.recomputeGroup(g)
	if g.members.Cardinality() == 0 {
		b.groups.Delete(key.Id())
	}
}

func (b *BaseChainSupervisor) recomputeGroup(g *groupState) {
	prev := g.state.Load()
	next := recomputeState(prev, b.groupMemberStates(g), b.subChainMethods)
	wrappers := prev.Compare(next)
	g.state.Store(next)
	if len(wrappers) > 0 {
		b.subGroupStateManager.Publish(&ChainSupervisorStateWrapperEvent{Wrappers: wrappers, NodeGroupId: g.id})
	}
	// no lag or dimension tracking here - that stays network-level
}

func (b *BaseChainSupervisor) groupMemberStates(g *groupState) []*protocol.UpstreamState {
	states := make([]*protocol.UpstreamState, 0, g.members.Cardinality())
	for _, id := range g.members.ToSlice() {
		if state, ok := b.upstreamStates.Load(id); ok {
			states = append(states, state)
		}
	}
	return states
}

func (b *BaseChainSupervisor) updateGroupHead(id string, headEvent *protocol.HeadUpstreamEvent) {
	membership, ok := b.upstreamGroup[id]
	if !ok {
		// head before the first state event: the group is seeded on assignGroup
		return
	}
	if g, gok := b.groups.Load(membership.key.Id()); gok {
		b.updateGroupHeadIn(g, id, headEvent)
	}
}

// updateGroupHeadIn mirrors updateHead against the group's own fork choice and
// Atomic state, minus lag tracking.
func (b *BaseChainSupervisor) updateGroupHeadIn(g *groupState, id string, headEvent *protocol.HeadUpstreamEvent) {
	if headEvent == nil {
		return
	}
	updated, head := g.fc.Choose(id, headEvent)
	if !updated {
		return
	}
	state := g.state.Load()
	state.HeadData = NewChainHeadData(head, id)
	g.state.Store(state)
	if !state.HeadData.IsEmpty() {
		b.subGroupStateManager.Publish(&ChainSupervisorStateWrapperEvent{
			Wrappers:    []ChainSupervisorStateWrapper{NewHeadWrapper(state.HeadData.Head, id)},
			NodeGroupId: g.id,
		})
	}
}
