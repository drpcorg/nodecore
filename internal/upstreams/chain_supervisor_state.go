package upstreams

import (
	"maps"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
)

type ChainHeadData struct {
	Head       protocol.Block
	UpstreamId string
}

func NewChainHeadData(head protocol.Block, upstreamId string) ChainHeadData {
	return ChainHeadData{
		Head:       head,
		UpstreamId: upstreamId,
	}
}

func (c ChainHeadData) IsEmpty() bool {
	return c.Head.IsEmptyByHeight()
}

type AggregatedLabels struct {
	Amount int
	Labels map[string]string
}

func NewAggregatedLabels(amount int, labels map[string]string) AggregatedLabels {
	return AggregatedLabels{
		Amount: amount,
		Labels: labels,
	}
}

func (a AggregatedLabels) Equals(other AggregatedLabels) bool {
	return a.Amount == other.Amount && maps.Equal(a.Labels, other.Labels)
}

func CompareAggregatedLabels(a, b []AggregatedLabels) bool {
	if len(a) != len(b) {
		return false
	}
	for _, labelA := range a {
		_, ok := lo.Find(b, func(item AggregatedLabels) bool {
			return item.Equals(labelA)
		})
		if !ok {
			return false
		}
	}
	return true
}

type ChainSupervisorState struct {
	Status      protocol.AvailabilityStatus
	HeadData    ChainHeadData
	Methods     methods.Methods
	Blocks      map[protocol.BlockType]protocol.Block
	LowerBounds map[protocol.LowerBoundType]protocol.LowerBoundData
	ChainLabels []AggregatedLabels
	SubMethods  mapset.Set[string]
	// Caps is the union of subscription capabilities across available upstreams
	// (WsCap, and for EVM NewHeadsCap/LogsCap). The subscription engine reads it
	// to decide whether a topic can be served locally.
	Caps mapset.Set[protocol.Cap]
}

func (c ChainSupervisorState) Compare(new ChainSupervisorState) []ChainSupervisorStateWrapper {
	wrappers := make([]ChainSupervisorStateWrapper, 0)

	if c.Status != new.Status {
		wrappers = append(wrappers, NewStatusWrapper(new.Status))
	}

	// identity first: GetSupportedMethods clones the set
	if c.Methods != new.Methods && !c.Methods.GetSupportedMethods().Equal(new.Methods.GetSupportedMethods()) {
		wrappers = append(wrappers, NewMethodsWrapper(new.Methods.GetSupportedMethods().ToSlice()))
	}

	compareBlocksFunc := func(b1, b2 protocol.Block) bool {
		return b1.Equals(b2)
	}
	if !maps.EqualFunc(c.Blocks, new.Blocks, compareBlocksFunc) {
		wrappers = append(wrappers, NewBlocksWrapper(new.Blocks))
	}

	if !maps.Equal(c.LowerBounds, new.LowerBounds) {
		wrappers = append(wrappers, NewLowerBoundsWrapper(lo.Values(new.LowerBounds)))
	}

	if !CompareAggregatedLabels(c.ChainLabels, new.ChainLabels) {
		wrappers = append(wrappers, NewLabelsWrapper(new.ChainLabels))
	}

	if !c.SubMethods.Equal(new.SubMethods) {
		wrappers = append(wrappers, NewSubMethodsWrapper(new.SubMethods.ToSlice()))
	}

	// Caps changes (e.g. the last NewHeadsCap upstream disconnecting) must be
	// observable so locally-synthesized subscriptions can fail over instead of
	// starving silently.
	if !capsEqual(c.Caps, new.Caps) {
		wrappers = append(wrappers, NewCapsWrapper(new.Caps))
	}

	return wrappers
}

func capsEqual(a, b mapset.Set[protocol.Cap]) bool {
	if a == nil || b == nil {
		return (a == nil || a.Cardinality() == 0) && (b == nil || b.Cardinality() == 0)
	}
	return a.Equal(b)
}

// stateFacets are the parts of a merged state, so that a member update
// recomputes only the parts it touched.
type stateFacets uint8

const (
	statusFacet stateFacets = 1 << iota
	methodsFacet
	capsFacet
	blocksFacet
	boundsFacet
	labelsFacet

	allFacets = statusFacet | methodsFacet | capsFacet | blocksFacet | boundsFacet | labelsFacet
)

// changedFacets compares the copy-on-write parts of two snapshots by identity.
func changedFacets(prev, next *protocol.UpstreamState) stateFacets {
	wasAvailable, isAvailable := prev.Status == protocol.Available, next.Status == protocol.Available
	if wasAvailable != isAvailable {
		// only available upstreams are merged
		return allFacets
	}
	var changed stateFacets
	if prev.Status != next.Status {
		changed |= statusFacet
	}
	if !isAvailable {
		return changed
	}
	if prev.UpstreamMethods != next.UpstreamMethods {
		changed |= methodsFacet
	}
	if prev.Caps != next.Caps {
		changed |= capsFacet
	}
	if prev.BlockInfo != next.BlockInfo {
		changed |= blocksFacet
	}
	if prev.LowerBoundsInfo != next.LowerBoundsInfo {
		changed |= boundsFacet
	}
	if prev.Labels != next.Labels {
		changed |= labelsFacet
	}
	return changed
}

// recomputeFacets rebuilds the changed facets of a merged view (network or
// group) from the member snapshots; the rest, HeadData included, carries over
// from prev.
func recomputeFacets(
	prev ChainSupervisorState,
	states []*protocol.UpstreamState,
	changed stateFacets,
	subChainMethods mapset.Set[string],
) ChainSupervisorState {
	next := prev
	// the status covers every member, the other facets only the available ones
	if changed&statusFacet != 0 {
		next.Status = minStatus(states)
	}
	if changed&^statusFacet == 0 {
		return next
	}
	available := lo.Filter(states, func(item *protocol.UpstreamState, _ int) bool {
		return item.Status == protocol.Available
	})
	if changed&methodsFacet != 0 {
		next.Methods = processUpstreamMethods(available)
	}
	if changed&blocksFacet != 0 {
		next.Blocks = processUpstreamBlocks(available)
	}
	if changed&boundsFacet != 0 {
		next.LowerBounds = processLowerBounds(available)
	}
	if changed&labelsFacet != 0 {
		next.ChainLabels = processLabels(available)
	}
	if changed&capsFacet != 0 {
		next.Caps = processCaps(available)
	}
	if changed&(methodsFacet|capsFacet) != 0 {
		next.SubMethods = processSubMethods(subChainMethods, next.Methods, next.Caps)
	}
	return next
}

// chooseHead runs a head event through a node group's fork choice, the only
// place a group head changes, and publishes the new head.
func chooseHead(
	fc choice.ForkChoice,
	state *utils.Atomic[ChainSupervisorState],
	subs *utils.SubscriptionManager[*ChainSupervisorStateWrapperEvent],
	nodeGroupId string,
	upstreamId string,
	headEvent *protocol.HeadUpstreamEvent,
) {
	updated, head := fc.Choose(upstreamId, headEvent)
	if !updated {
		return
	}
	next := state.Load()
	next.HeadData = NewChainHeadData(head, upstreamId)
	state.Store(next)
	if !next.HeadData.IsEmpty() {
		subs.Publish(&ChainSupervisorStateWrapperEvent{
			Wrappers:    []ChainSupervisorStateWrapper{NewHeadWrapper(head, upstreamId)},
			NodeGroupId: nodeGroupId,
		})
	}
}

func minStatus(states []*protocol.UpstreamState) protocol.AvailabilityStatus {
	var status = protocol.Unavailable
	for _, state := range states {
		if state.Status < status {
			status = state.Status
		}
	}
	return status
}

func processSubMethods(subChainMethods mapset.Set[string], chainMethods methods.Methods, caps mapset.Set[protocol.Cap]) mapset.Set[string] {
	subMethods := mapset.NewThreadUnsafeSet[string]()
	for name := range subChainMethods.Iter() {
		// Only a method some available upstream actually supports (after config,
		// detection and bans) can be advertised: a disabled subscribe method is
		// the operator saying "no subscriptions from this upstream".
		method := chainMethods.GetMethod(name)
		if method == nil {
			continue
		}
		// A gRPC stream rides the grpc connector the spec binds it to (all grpc
		// calls share the one connection); a JSON-RPC subscription additionally
		// needs a live websocket connector somewhere.
		if method.GrpcCallType().IsServerStream() || (caps != nil && caps.Contains(protocol.WsCap)) {
			subMethods.Add(name)
		}
	}
	// EVM advertises concrete topics derived from caps instead of the generic
	// eth_subscribe method, so SubscribeChainStatus and NativeSubscribe see the
	// real sub types. A topic is offered only if it can be served locally
	// (newHeads -> NewHeadsCap, logs -> LogsCap, newPendingTransactions and
	// drpc_pendingTransactions -> PendingTxCap).
	if subMethods.ContainsOne("eth_subscribe") {
		subMethods.Remove("eth_subscribe")
		if caps.Contains(protocol.NewHeadsCap) {
			subMethods.Add("newHeads")
		}
		if caps.Contains(protocol.LogsCap) {
			subMethods.Add("logs")
		}
		if caps.Contains(protocol.PendingTxCap) {
			subMethods.Add("newPendingTransactions")
			subMethods.Add("drpc_pendingTransactions")
		}
	}
	return subMethods
}

func processCaps(availableUpstreams []*protocol.UpstreamState) mapset.Set[protocol.Cap] {
	caps := mapset.NewThreadUnsafeSet[protocol.Cap]()
	for _, upState := range availableUpstreams {
		if upState.Caps == nil {
			continue
		}
		caps = caps.Union(upState.Caps)
	}
	return caps
}

func processLabels(availableUpstreams []*protocol.UpstreamState) []AggregatedLabels {
	allLabels := make([]AggregatedLabels, 0)

	for _, upState := range availableUpstreams {
		if upState.Labels == nil {
			continue
		}
		upLabels := upState.Labels.GetAllLabels()
		if len(upLabels) == 0 {
			continue
		}

		_, idx, ok := lo.FindIndexOf(allLabels, func(item AggregatedLabels) bool {
			return maps.Equal(upLabels, item.Labels)
		})
		if !ok {
			allLabels = append(allLabels, NewAggregatedLabels(1, upLabels))
		} else {
			allLabels[idx].Amount++
		}
	}
	return allLabels
}

func processUpstreamMethods(availableStates []*protocol.UpstreamState) methods.Methods {
	delegates := lo.Map(availableStates, func(item *protocol.UpstreamState, index int) methods.Methods {
		return item.UpstreamMethods
	})

	return methods.NewChainMethods(delegates)
}

func processLowerBounds(availableStates []*protocol.UpstreamState) map[protocol.LowerBoundType]protocol.LowerBoundData {
	bounds := make(map[protocol.LowerBoundType]protocol.LowerBoundData)

	for _, upsState := range availableStates {
		if upsState.LowerBoundsInfo == nil {
			continue
		}
		upBounds := upsState.LowerBoundsInfo.GetAllBounds()
		for _, bound := range upBounds {
			currentBound, ok := bounds[bound.Type]
			if !ok || bound.Bound < currentBound.Bound {
				bounds[bound.Type] = bound
			}
		}
	}

	return bounds
}

func processUpstreamBlocks(availableStates []*protocol.UpstreamState) map[protocol.BlockType]protocol.Block {
	blocks := make(map[protocol.BlockType]protocol.Block, len(availableStates))

	for _, upState := range availableStates {
		if upState.BlockInfo != nil {
			upBlocks := upState.BlockInfo.GetBlocks()

			for blockType, blockData := range upBlocks {
				currentBlockData, ok := blocks[blockType]
				if !ok {
					blocks[blockType] = blockData
				} else {
					blocks[blockType] = compareBlocks(blockType, currentBlockData, blockData)
				}
			}
		}
	}

	return blocks
}

func compareBlocks(blockType protocol.BlockType, currentBlock, newBlock protocol.Block) protocol.Block {
	switch blockType {
	case protocol.FinalizedBlock, protocol.SafeBlock:
		if newBlock.Height > currentBlock.Height {
			return newBlock
		}
	}
	return currentBlock
}
