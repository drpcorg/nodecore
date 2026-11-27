package emerald

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
)

// upstreamStatusChain is one chain of one stream. Its goroutine owns
// everything in it.
type upstreamStatusChain struct {
	chain     chains.Chain
	grpcId    int
	feed      upstreams.UpstreamStateFeed
	responses chan<- *dshackle.SubscribeUpstreamStatusResponse

	// minInterval is the least time between two responses about one upstream;
	// 0 turns the throttle off.
	minInterval time.Duration
	// throttleTimer fires when the first held upstream is due.
	throttleTimer *time.Timer

	records map[string]*upstreamRecord
	// snapshotRevision covers the upstreams that have no record: a snapshot
	// says about them that they did not exist.
	snapshotRevision uint64
}

// upstreamRecord is what the stream knows about one upstream.
type upstreamRecord struct {
	// announced: the client has been sent a Full for the instance with index.
	announced bool
	index     string
	// sentHead is the last head sent, so a resync can leave an unchanged head out.
	sentHead protocol.Block
	// covered is the revision of the last read that covered this upstream;
	// an event at or below it is already in what that read returned.
	covered uint64
	pending *upstreamPending
	// lastSent is when the last response about this upstream went out; zero
	// for an upstream nothing was sent about, which is therefore due at once.
	lastSent time.Time
}

// upstreamPending is what is waiting to be sent about one upstream: a Full,
// changes, or its removal.
type upstreamPending struct {
	full    *upstreams.UpstreamView
	changes []upstreams.UpstreamStateWrapper
	removed bool
}

func newUpstreamStatusChain(
	chain chains.Chain,
	grpcId int,
	feed upstreams.UpstreamStateFeed,
	minInterval time.Duration,
	responses chan<- *dshackle.SubscribeUpstreamStatusResponse,
) *upstreamStatusChain {
	return &upstreamStatusChain{
		chain:       chain,
		grpcId:      grpcId,
		feed:        feed,
		responses:   responses,
		minInterval: minInterval,
		records:     make(map[string]*upstreamRecord),
	}
}

func (c *upstreamStatusChain) run(ctx context.Context, resyncInterval time.Duration) {
	// subscribing before the first read leaves no gap: whatever changes after
	// the read is in the queue, and whatever is older is dropped by revision
	sub := c.feed.SubscribeUpstreamStates(fmt.Sprintf("upstream_status_%s", c.chain))
	defer sub.Unsubscribe()

	// created stopped: flush arms it only while an upstream is held
	c.throttleTimer = time.NewTimer(time.Hour)
	c.throttleTimer.Stop()
	defer c.throttleTimer.Stop()

	c.applySnapshot(c.feed.GetUpstreamViews())
	if !c.flush(ctx) {
		return
	}

	resyncTicker := time.NewTicker(resyncInterval)
	defer resyncTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			c.applyEvent(event)
		case <-resyncTicker.C:
			c.applySnapshot(c.feed.GetUpstreamViews())
		case <-c.throttleTimer.C:
		}
		if !c.flush(ctx) {
			return
		}
	}
}

// applySnapshot replaces whatever is pending with what the snapshot says. It
// also serves the start of the stream, where nothing is announced yet.
func (c *upstreamStatusChain) applySnapshot(snapshot upstreams.UpstreamViewsSnapshot) {
	c.snapshotRevision = snapshot.Revision

	for upstreamId, view := range snapshot.Views {
		record := c.record(upstreamId)
		record.covered = snapshot.Revision
		if record.announced && record.index == view.State.UpstreamIndex {
			record.pending = &upstreamPending{changes: resyncChanges(upstreamId, view, record.sentHead)}
			continue
		}
		c.offerInstance(upstreamId, record, view)
	}

	for upstreamId, record := range c.records {
		if _, present := snapshot.Views[upstreamId]; present {
			continue
		}
		record.covered = snapshot.Revision
		c.markRemoved(upstreamId, record)
	}
}

func (c *upstreamStatusChain) applyEvent(event *upstreams.UpstreamStateEvent) {
	record, known := c.records[event.UpstreamId]
	covered := c.snapshotRevision
	if known {
		covered = record.covered
	}
	if event.Revision <= covered {
		return
	}

	if event.Removed {
		if known {
			c.markRemoved(event.UpstreamId, record)
		}
		return
	}

	// A change can only be added to other changes. Behind a held removal or a
	// held Full the upstream is read again, so the change is not lost: the
	// removal may be over, and a Full is built from a whole upstream.
	holdsMoreThanChanges := known && record.pending != nil && (record.pending.removed || record.pending.full != nil)
	if known && record.announced && record.index == event.UpstreamIndex && !holdsMoreThanChanges {
		if record.pending == nil {
			record.pending = &upstreamPending{}
		}
		record.pending.changes = mergeChanges(record.pending.changes, event.Changes)
		return
	}

	// An upstream the client does not know, or another instance under a known
	// id: a Full needs the whole upstream, and an event carries only changes.
	view, revision, exists := c.feed.GetUpstreamView(event.UpstreamId)
	if !known {
		record = c.record(event.UpstreamId)
	}
	record.covered = revision
	if !exists {
		c.markRemoved(event.UpstreamId, record)
		return
	}
	c.offerInstance(event.UpstreamId, record, view)
}

// offerInstance handles an instance the client has not been sent a Full for.
// With a head its Full becomes pending. Without one nothing can be announced
// yet, and if the client still holds another instance under this id, that one
// is gone.
func (c *upstreamStatusChain) offerInstance(upstreamId string, record *upstreamRecord, view upstreams.UpstreamView) {
	if !view.State.HeadData.IsEmptyByHeight() {
		record.pending = &upstreamPending{full: &view}
		return
	}
	c.markRemoved(upstreamId, record)
}

// markRemoved makes the removal of an announced upstream pending and forgets
// an upstream the client never heard of.
func (c *upstreamStatusChain) markRemoved(upstreamId string, record *upstreamRecord) {
	if record.announced {
		record.pending = &upstreamPending{removed: true}
		return
	}
	delete(c.records, upstreamId)
}

func (c *upstreamStatusChain) record(upstreamId string) *upstreamRecord {
	record, known := c.records[upstreamId]
	if !known {
		record = &upstreamRecord{covered: c.snapshotRevision}
		c.records[upstreamId] = record
	}
	return record
}

// flush sends what is due and arms the timer for what is held back. It reports
// false when the stream is over.
func (c *upstreamStatusChain) flush(ctx context.Context) bool {
	earliestHeld, ok := c.sendDue(ctx, time.Now())
	if !ok {
		return false
	}
	c.armThrottle(earliestHeld)
	return true
}

// dueAt is when the next response about an upstream may go out: minInterval
// after the last one. The interval counts from that send, not from when a held
// update arrived, so two responses about one upstream are never closer than
// it. Nothing bypasses this: an upstream never sent about has no last send and
// is simply due at once.
func (c *upstreamStatusChain) dueAt(record *upstreamRecord) time.Time {
	return record.lastSent.Add(c.minInterval)
}

// sendDue sends the pending update of every upstream that is due and leaves
// the rest pending. It returns when the first of the held ones is due, the
// zero time if it held none, and false when the stream is over.
func (c *upstreamStatusChain) sendDue(ctx context.Context, now time.Time) (time.Time, bool) {
	var earliestHeld time.Time
	for upstreamId, record := range c.records {
		if record.pending == nil {
			continue
		}
		if due := c.dueAt(record); now.Before(due) {
			if earliestHeld.IsZero() || due.Before(earliestHeld) {
				earliestHeld = due
			}
			continue
		}
		response := c.takePending(upstreamId, record)
		// nothing to say moves nothing: the upstream stays due
		if response == nil {
			continue
		}
		record.lastSent = now
		if !sendResponse(ctx, c.responses, response) {
			return earliestHeld, false
		}
	}
	return earliestHeld, true
}

// armThrottle sets the timer for when the first held upstream is due, and
// stops it when nothing is held.
func (c *upstreamStatusChain) armThrottle(earliestHeld time.Time) {
	if earliestHeld.IsZero() {
		c.throttleTimer.Stop()
		return
	}
	c.throttleTimer.Reset(time.Until(earliestHeld))
}

// takePending turns the pending update of one upstream into its response and
// updates the record as if it was sent.
func (c *upstreamStatusChain) takePending(upstreamId string, record *upstreamRecord) *dshackle.SubscribeUpstreamStatusResponse {
	pending := record.pending
	record.pending = nil

	switch {
	case pending.removed:
		delete(c.records, upstreamId)
		return c.response(upstreamId, record.index, []upstreams.UpstreamStateWrapper{upstreams.NewStatusWrapper(protocol.Unavailable)})
	case pending.full != nil:
		record.announced = true
		record.index = pending.full.State.UpstreamIndex
		record.sentHead = pending.full.State.HeadData
		response := c.response(upstreamId, record.index, pending.full.Wrappers(upstreamId))
		response.FullResponse = true
		response.BuildInfo = &dshackle.BuildInfo{Version: buildinfo.ProductVersion()}
		return response
	case len(pending.changes) == 0:
		return nil
	default:
		for _, change := range pending.changes {
			if head, isHead := change.(*upstreams.HeadWrapper); isHead {
				record.sentHead = head.Head
			}
		}
		return c.response(upstreamId, record.index, pending.changes)
	}
}

func (c *upstreamStatusChain) response(upstreamId, index string, wrappers []upstreams.UpstreamStateWrapper) *dshackle.SubscribeUpstreamStatusResponse {
	return &dshackle.SubscribeUpstreamStatusResponse{
		ChainDescription: wrappersToChainDescription(c.grpcId, wrappers),
		UpstreamId:       upstreamId,
		UpstreamIndex:    index,
	}
}

// resyncChanges is the whole upstream again, without its head unless the head
// differs from the last one sent: a client takes every head it receives as a
// new one.
func resyncChanges(upstreamId string, view upstreams.UpstreamView, sentHead protocol.Block) []upstreams.UpstreamStateWrapper {
	headChanged := !view.State.HeadData.Equals(sentHead)
	changes := make([]upstreams.UpstreamStateWrapper, 0, 7)
	for _, wrapper := range view.Wrappers(upstreamId) {
		if _, isHead := wrapper.(*upstreams.HeadWrapper); isHead && !headChanged {
			continue
		}
		changes = append(changes, wrapper)
	}
	return changes
}

// mergeChanges adds later changes to earlier ones; a later change replaces an
// earlier one of the same kind.
func mergeChanges(earlier, later []upstreams.UpstreamStateWrapper) []upstreams.UpstreamStateWrapper {
	for _, change := range later {
		replaced := false
		for i, existing := range earlier {
			if changeKind(existing) == changeKind(change) {
				earlier[i] = change
				replaced = true
				break
			}
		}
		if !replaced {
			earlier = append(earlier, change)
		}
	}
	return earlier
}

func changeKind(change upstreams.UpstreamStateWrapper) string {
	switch change.(type) {
	case *upstreams.StatusWrapper:
		return "status"
	case *upstreams.MethodsWrapper:
		return "methods"
	case *upstreams.LowerBoundsWrapper:
		return "lowerBounds"
	case *upstreams.HeadWrapper:
		return "head"
	case *upstreams.BlocksWrapper:
		return "blocks"
	case *upstreams.SubMethodsWrapper:
		return "subMethods"
	case *upstreams.LabelsWrapper:
		return "labels"
	default:
		return fmt.Sprintf("%T", change)
	}
}
