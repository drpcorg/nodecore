package upstreams

import (
	"context"
	"maps"
	"slices"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
)

// UpstreamStateEvent describes one upstream after network lag validation. Metadata
// and head changes are separate events because existing clients consume heads separately.
type UpstreamStateEvent struct {
	Chain     chains.Chain
	ID, Index string
	Full      bool
	State     ChainSupervisorState
	Wrappers  []ChainSupervisorStateWrapper
}

type upstreamStateChange struct {
	Revision uint64
	Removed  bool
	ID       string
	State    *protocol.UpstreamState
}

type upstreamStateSnapshot struct {
	revision uint64
	states   map[string]*protocol.UpstreamState
	sub      *utils.Subscription[upstreamStateChange]
}

type upstreamStateRequest struct {
	name      string
	subscribe bool
	reply     chan upstreamStateSnapshot
}

// Registration and snapshots share the event loop with updates. In particular,
// no older queued change can follow the initial snapshot. The revision also
// lets a resync skip changes already incorporated into its snapshot.
func (b *GenericChainSupervisor) handleUpstreamStateRequest(request upstreamStateRequest) {
	snapshot := upstreamStateSnapshot{states: make(map[string]*protocol.UpstreamState), revision: b.upstreamStateRevision}
	b.upstreamStates.Range(func(id string, state *protocol.UpstreamState) bool {
		snapshot.states[id] = state
		return true
	})
	if request.subscribe {
		snapshot.sub = b.subUpstreamStateManager.Subscribe(request.name)
	}
	request.reply <- snapshot
}

func (b *GenericChainSupervisor) requestUpstreamStates(ctx context.Context, name string, subscribe bool) (upstreamStateSnapshot, bool) {
	reply := make(chan upstreamStateSnapshot, 1)
	select {
	case <-ctx.Done():
		return upstreamStateSnapshot{}, false
	case <-b.ctx.Done():
		return upstreamStateSnapshot{}, false
	case b.upstreamStateRequests <- upstreamStateRequest{name: name, subscribe: subscribe, reply: reply}:
	}
	// Once accepted, consume the buffered reply even if cancellation wins, so a
	// newly registered subscription is always released.
	snapshot := <-reply
	return snapshot, true
}

// SubscribeUpstreamStates uses bounded, nonblocking fan-out from the supervisor.
// Each worker owns its coalescing and last-sent state; a slow output cannot stall
// the supervisor. Authoritative resyncs include tombstones to repair dropped removals.
func (b *GenericChainSupervisor) SubscribeUpstreamStates(ctx context.Context, name string, interval, resync time.Duration, output chan<- *UpstreamStateEvent) {
	go b.forwardUpstreamStates(ctx, name, interval, resync, output)
}

func (b *GenericChainSupervisor) forwardUpstreamStates(ctx context.Context, name string, interval, resync time.Duration, output chan<- *UpstreamStateEvent) {
	snapshot, ok := b.requestUpstreamStates(ctx, name, true)
	if !ok {
		return
	}
	defer snapshot.sub.Unsubscribe()

	sent := make(map[string]*protocol.UpstreamState)
	pending := snapshot.states
	revision := snapshot.revision
	send := func(id string, state *protocol.UpstreamState, periodic bool) bool {
		previous := sent[id]
		emit := func(event *UpstreamStateEvent) bool {
			event.Chain, event.ID, event.Index = b.chain, id, state.UpstreamIndex
			select {
			case <-ctx.Done():
				return false
			case <-b.ctx.Done():
				return false
			case output <- event:
				return true
			}
		}
		if previous == nil {
			if !emit(&UpstreamStateEvent{Full: true, State: upstreamChainState(b.chain, id, state)}) {
				return false
			}
		} else {
			if periodic || upstreamDescriptionChanged(previous, state) {
				if !emit(&UpstreamStateEvent{Wrappers: upstreamChainState(b.chain, id, state).SnapshotWrappers()}) {
					return false
				}
			}
			if !previous.HeadData.Equals(state.HeadData) && !state.HeadData.IsEmptyByHeight() {
				if !emit(&UpstreamStateEvent{Wrappers: []ChainSupervisorStateWrapper{NewHeadWrapper(state.HeadData, id)}}) {
					return false
				}
			}
		}
		sent[id] = state
		return true
	}
	flush := func(periodic bool) bool {
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			if !send(id, pending[id], periodic) {
				return false
			}
		}
		clear(pending)
		return true
	}
	if !flush(false) {
		return
	}
	lastSent := time.Now()
	ticker := time.NewTicker(resync)
	defer ticker.Stop()
	gap := time.NewTimer(time.Hour)
	gap.Stop()
	defer gap.Stop()
	var due <-chan time.Time
	periodic := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.ctx.Done():
			return
		case change, open := <-snapshot.sub.Events:
			if !open {
				return
			}
			if change.Revision <= revision {
				continue
			}
			// An upstream's first full description bypasses throttling.
			if sent[change.ID] == nil {
				if change.Removed {
					continue
				}
				if !send(change.ID, change.State, false) {
					return
				}
				continue
			}
			pending[change.ID] = change.State
		case <-ticker.C:
			current, alive := b.requestUpstreamStates(ctx, "", false)
			if !alive {
				return
			}
			revision = current.revision
			pending = current.states
			for id, previous := range sent {
				if _, present := pending[id]; !present {
					tombstone := protocol.DefaultUpstreamState(nil, nil, previous.UpstreamIndex, nil, nil)
					tombstone.Status = protocol.Unavailable
					pending[id] = &tombstone
				}
			}
			periodic = true
		case <-due:
			due = nil
		}
		if len(pending) == 0 {
			continue
		}
		if wait := time.Until(lastSent.Add(interval)); wait > 0 {
			if due == nil {
				gap.Reset(wait)
				due = gap.C
			}
			continue
		}
		if !flush(periodic) {
			return
		}
		periodic = false
		lastSent = time.Now()
		if due != nil {
			gap.Stop()
			due = nil
		}
	}
}

func upstreamDescriptionChanged(a, b *protocol.UpstreamState) bool {
	return a.Status != b.Status || a.UpstreamIndex != b.UpstreamIndex || a.UpstreamMethods != b.UpstreamMethods || a.Caps != b.Caps || a.BlockInfo != b.BlockInfo || a.LowerBoundsInfo != b.LowerBoundsInfo || a.Labels != b.Labels
}

// upstreamChainState describes one upstream using the existing domain state.
// Status already includes network lag validation; no per-upstream lag is recomputed.
func upstreamChainState(chain chains.Chain, id string, state *protocol.UpstreamState) ChainSupervisorState {
	callMethods := state.UpstreamMethods
	if callMethods == nil {
		callMethods = methods.NewChainMethods(nil)
	}
	caps := state.Caps
	if caps == nil {
		caps = mapset.NewThreadUnsafeSet[protocol.Cap]()
	}
	result := ChainSupervisorState{Status: state.Status, HeadData: NewChainHeadData(state.HeadData, id), Methods: callMethods, Caps: caps, LowerBounds: map[protocol.LowerBoundType]protocol.LowerBoundData{}}
	if state.BlockInfo != nil {
		result.Blocks = state.BlockInfo.GetBlocks()
	}
	if state.LowerBoundsInfo != nil {
		for _, bound := range state.LowerBoundsInfo.GetAllBounds() {
			result.LowerBounds[bound.Type] = bound
		}
	}
	if state.Labels != nil {
		result.ChainLabels = []AggregatedLabels{NewAggregatedLabels(1, state.Labels.GetAllLabels())}
	}
	result.SubMethods = ProcessSubMethods(specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain)), callMethods, caps)
	return result
}
