package upstreams

import (
	"maps"
	"sync"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// UpstreamStateWrapper is one changed part of an upstream. It is the chain-level
// wrapper type under a name that fits an upstream; the wrapper structs are shared.
type UpstreamStateWrapper = ChainSupervisorStateWrapper

// UpstreamView is one upstream as its status stream reports it: its own state
// plus the one thing the state does not hold.
type UpstreamView struct {
	// State is the pointer the supervisor holds; it is never modified.
	State *protocol.UpstreamState
	// SubMethods are derived from the upstream's methods and caps.
	SubMethods mapset.Set[string]
}

// Wrappers is the whole upstream as seven wrappers, in the order of the chain
// status full response: status, methods, lower bounds, head, blocks,
// subscription methods, labels.
func (v UpstreamView) Wrappers(upstreamId string) []UpstreamStateWrapper {
	return []UpstreamStateWrapper{
		NewStatusWrapper(v.State.Status),
		NewMethodsWrapper(supportedMethodsOf(v.State).ToSlice()),
		NewLowerBoundsWrapper(lowerBoundsOf(v.State)),
		NewHeadWrapper(v.State.HeadData, upstreamId),
		NewBlocksWrapper(blocksOf(v.State)),
		NewSubMethodsWrapper(subMethodsSlice(v.SubMethods)),
		NewLabelsWrapper(labelsOf(v.State)),
	}
}

// UpstreamStateEvent is one change of one upstream of a chain. Revision orders
// it against reads: a read that returns revision R already contains every
// event numbered R or lower.
type UpstreamStateEvent struct {
	UpstreamId    string
	UpstreamIndex string
	Revision      uint64
	Removed       bool
	// Changes holds only what this event changed; empty for a removal.
	Changes []UpstreamStateWrapper
}

type UpstreamViewsSnapshot struct {
	Revision uint64
	Views    map[string]UpstreamView
}

// UpstreamStateFeed is the per-upstream side of one chain: its changes and
// reads of it.
type UpstreamStateFeed interface {
	SubscribeUpstreamStates(name string) *utils.Subscription[*UpstreamStateEvent]
	// GetUpstreamViews returns every upstream of the chain.
	GetUpstreamViews() UpstreamViewsSnapshot
	// GetUpstreamView returns one upstream and the revision of the read; false
	// when the supervisor holds no such upstream.
	GetUpstreamView(upstreamId string) (UpstreamView, uint64, bool)
}

var _ UpstreamStateFeed = (*GenericChainSupervisor)(nil)

func (b *GenericChainSupervisor) SubscribeUpstreamStates(name string) *utils.Subscription[*UpstreamStateEvent] {
	return b.upstreamFeed.subscribe(name)
}

func (b *GenericChainSupervisor) GetUpstreamViews() UpstreamViewsSnapshot {
	states := make(map[string]*protocol.UpstreamState)

	b.upstreamFeed.lock()
	b.upstreamStates.Range(func(upstreamId string, state *protocol.UpstreamState) bool {
		states[upstreamId] = state
		return true
	})
	revision := b.upstreamFeed.currentRevision()
	b.upstreamFeed.unlock()

	views := make(map[string]UpstreamView, len(states))
	for upstreamId, state := range states {
		views[upstreamId] = b.upstreamView(state)
	}
	return UpstreamViewsSnapshot{Revision: revision, Views: views}
}

func (b *GenericChainSupervisor) GetUpstreamView(upstreamId string) (UpstreamView, uint64, bool) {
	b.upstreamFeed.lock()
	state, known := b.upstreamStates.Load(upstreamId)
	revision := b.upstreamFeed.currentRevision()
	b.upstreamFeed.unlock()

	if !known {
		return UpstreamView{}, revision, false
	}
	return b.upstreamView(state), revision, true
}

// upstreamFeedBuffer is per subscriber: every head of every upstream of the
// chain goes through it.
const upstreamFeedBuffer = 1000

// upstreamStateFeed is what the supervisor needs to publish per-upstream
// changes and answer consistent reads. mu guards revision and every write to
// upstreamStates, so a read under mu sees the states and the revision of the
// same moment. Readers that need no revision (matchers, the chain aggregation)
// keep reading upstreamStates without it.
type upstreamStateFeed struct {
	mu       sync.Mutex
	revision uint64
	manager  *utils.SubscriptionManager[*UpstreamStateEvent]
}

func newUpstreamStateFeed() *upstreamStateFeed {
	return &upstreamStateFeed{
		manager: utils.NewSubscriptionManager[*UpstreamStateEvent]("chain_supervisor_upstream_events"),
	}
}

func (f *upstreamStateFeed) lock() {
	f.mu.Lock()
}

func (f *upstreamStateFeed) unlock() {
	f.mu.Unlock()
}

// increaseRevision returns the new revision. Call it with the lock held, in
// the same critical section as the write to upstreamStates it numbers.
func (f *upstreamStateFeed) increaseRevision() uint64 {
	f.revision++
	return f.revision
}

// currentRevision is for a read: call it with the lock held, next to the read
// of upstreamStates it belongs to.
func (f *upstreamStateFeed) currentRevision() uint64 {
	return f.revision
}

func (f *upstreamStateFeed) publish(event *UpstreamStateEvent) {
	f.manager.Publish(event)
}

func (f *upstreamStateFeed) subscribe(name string) *utils.Subscription[*UpstreamStateEvent] {
	return f.manager.SubscribeWithSize(name, upstreamFeedBuffer)
}

// storeUpstreamState is the only place a state is written to upstreamStates.
// Nothing but the write and the revision increment happens under the lock:
// the comparison and the publish run after it. headOnly says the caller
// changed nothing but the head, which skips the comparison of the rest.
func (b *GenericChainSupervisor) storeUpstreamState(upstreamId string, state *protocol.UpstreamState, headOnly bool) {
	old, known := b.upstreamStates.Load(upstreamId)

	b.upstreamFeed.lock()
	b.upstreamStates.Store(upstreamId, state)
	revision := b.upstreamFeed.increaseRevision()
	b.upstreamFeed.unlock()

	var changes []UpstreamStateWrapper
	switch {
	case !known:
		changes = b.upstreamView(state).Wrappers(upstreamId)
	case headOnly:
		if !old.HeadData.Equals(state.HeadData) {
			changes = []UpstreamStateWrapper{NewHeadWrapper(state.HeadData, upstreamId)}
		}
	default:
		changes = b.upstreamStateChanges(upstreamId, old, state)
	}
	if len(changes) == 0 {
		return
	}
	b.upstreamFeed.publish(&UpstreamStateEvent{
		UpstreamId:    upstreamId,
		UpstreamIndex: state.UpstreamIndex,
		Revision:      revision,
		Changes:       changes,
	})
}

// deleteUpstreamState is the only place a state is removed from upstreamStates.
func (b *GenericChainSupervisor) deleteUpstreamState(upstreamId string) {
	old, known := b.upstreamStates.Load(upstreamId)
	if !known {
		return
	}

	b.upstreamFeed.lock()
	b.upstreamStates.Delete(upstreamId)
	revision := b.upstreamFeed.increaseRevision()
	b.upstreamFeed.unlock()

	b.upstreamFeed.publish(&UpstreamStateEvent{
		UpstreamId:    upstreamId,
		UpstreamIndex: old.UpstreamIndex,
		Revision:      revision,
		Removed:       true,
	})
}

func (b *GenericChainSupervisor) upstreamView(state *protocol.UpstreamState) UpstreamView {
	return UpstreamView{State: state, SubMethods: b.upstreamSubMethods(state)}
}

// upstreamSubMethods resolves the methods the way the chain aggregation does
// for a chain of this one upstream, so both report a subscription method by
// the same rule.
func (b *GenericChainSupervisor) upstreamSubMethods(state *protocol.UpstreamState) mapset.Set[string] {
	if state.UpstreamMethods == nil {
		return mapset.NewThreadUnsafeSet[string]()
	}
	return b.processSubMethods(processUpstreamMethods([]*protocol.UpstreamState{state}), state.Caps)
}

// upstreamStateChanges compares two states of one upstream kind by kind, in
// the order of UpstreamView.Wrappers. BlockInfo, LowerBoundsInfo and Labels
// are replaced by a copy on every change and never modified in place, so the
// same pointer means the same content and skips the comparison.
func (b *GenericChainSupervisor) upstreamStateChanges(upstreamId string, old, updated *protocol.UpstreamState) []UpstreamStateWrapper {
	changes := make([]UpstreamStateWrapper, 0)

	if old.Status != updated.Status {
		changes = append(changes, NewStatusWrapper(updated.Status))
	}

	methodsChanged := false
	if !sameUpstreamMethods(old.UpstreamMethods, updated.UpstreamMethods) {
		updatedMethods := supportedMethodsOf(updated)
		if methodsChanged = !supportedMethodsOf(old).Equal(updatedMethods); methodsChanged {
			changes = append(changes, NewMethodsWrapper(updatedMethods.ToSlice()))
		}
	}

	if old.LowerBoundsInfo != updated.LowerBoundsInfo {
		updatedBounds := lowerBoundsOf(updated)
		if !maps.Equal(lowerBoundsByType(lowerBoundsOf(old)), lowerBoundsByType(updatedBounds)) {
			changes = append(changes, NewLowerBoundsWrapper(updatedBounds))
		}
	}

	if !old.HeadData.Equals(updated.HeadData) {
		changes = append(changes, NewHeadWrapper(updated.HeadData, upstreamId))
	}

	if old.BlockInfo != updated.BlockInfo {
		updatedBlocks := blocksOf(updated)
		if !maps.EqualFunc(blocksOf(old), updatedBlocks, protocol.Block.Equals) {
			changes = append(changes, NewBlocksWrapper(updatedBlocks))
		}
	}

	// the subscription methods depend on nothing but the methods and the caps
	if methodsChanged || !capsEqual(old.Caps, updated.Caps) {
		updatedSubMethods := b.upstreamSubMethods(updated)
		if !b.upstreamSubMethods(old).Equal(updatedSubMethods) {
			changes = append(changes, NewSubMethodsWrapper(subMethodsSlice(updatedSubMethods)))
		}
	}

	if old.Labels != updated.Labels {
		updatedLabels := labelsOf(updated)
		if !CompareAggregatedLabels(labelsOf(old), updatedLabels) {
			changes = append(changes, NewLabelsWrapper(updatedLabels))
		}
	}

	return changes
}

// sameUpstreamMethods reports that two states hold the very same methods
// object. An upstream's methods are built once and never modified: a ban, an
// unban or a detection result replaces the object. So the same object means
// the same methods, and the comparison of the two sets, which copies both, can
// be skipped. The pointers are compared, not the interface values: that cannot
// panic whatever a future implementation of Methods looks like. Any other
// implementation simply takes the full comparison.
func sameUpstreamMethods(old, updated methods.Methods) bool {
	oldMethods, isUpstreamMethods := old.(*methods.UpstreamMethods)
	if !isUpstreamMethods {
		return false
	}
	updatedMethods, isUpstreamMethods := updated.(*methods.UpstreamMethods)
	return isUpstreamMethods && oldMethods == updatedMethods
}

func supportedMethodsOf(state *protocol.UpstreamState) mapset.Set[string] {
	if state.UpstreamMethods == nil {
		return mapset.NewThreadUnsafeSet[string]()
	}
	return state.UpstreamMethods.GetSupportedMethods()
}

func lowerBoundsOf(state *protocol.UpstreamState) []protocol.LowerBoundData {
	if state.LowerBoundsInfo == nil {
		return []protocol.LowerBoundData{}
	}
	return state.LowerBoundsInfo.GetAllBounds()
}

func lowerBoundsByType(bounds []protocol.LowerBoundData) map[protocol.LowerBoundType]protocol.LowerBoundData {
	byType := make(map[protocol.LowerBoundType]protocol.LowerBoundData, len(bounds))
	for _, bound := range bounds {
		byType[bound.Type] = bound
	}
	return byType
}

func blocksOf(state *protocol.UpstreamState) map[protocol.BlockType]protocol.Block {
	if state.BlockInfo == nil {
		return map[protocol.BlockType]protocol.Block{}
	}
	return state.BlockInfo.GetBlocks()
}

// labelsOf is the upstream's labels in the shape the chain reports them: a
// list of label sets with a count. One upstream is one set, or none.
func labelsOf(state *protocol.UpstreamState) []AggregatedLabels {
	if state.Labels == nil {
		return []AggregatedLabels{}
	}
	labels := state.Labels.GetAllLabels()
	if len(labels) == 0 {
		return []AggregatedLabels{}
	}
	return []AggregatedLabels{NewAggregatedLabels(1, labels)}
}

func subMethodsSlice(subMethods mapset.Set[string]) []string {
	if subMethods == nil {
		return []string{}
	}
	return subMethods.ToSlice()
}
