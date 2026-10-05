package upstreams

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/rs/zerolog/log"
)

// HeadFeedEvent is what a filtered head feed emits; see ChainSupervisor.SubscribeHead.
type HeadFeedEvent interface {
	headFeedEvent()
}

// HeadUpdated is the feed's new head. UpstreamId is the upstream whose event
// produced it, the same convention as ChainHeadData.UpstreamId.
type HeadUpdated struct {
	Head       protocol.Block
	UpstreamId string
}

func (HeadUpdated) headFeedEvent() {}

// HeadFeedEmpty means no upstream passes the feed's filter any more. Whether
// that is terminal is the consumer's decision: the feed resumes with a
// HeadUpdated when a matching upstream comes back and reports a head.
type HeadFeedEmpty struct{}

func (HeadFeedEmpty) headFeedEvent() {}

// HeadFeedSubscription is one consumer's view of a filtered head feed.
type HeadFeedSubscription struct {
	Events      <-chan HeadFeedEvent
	unsubscribe func()
}

// NewHeadFeedSubscription wraps a channel and an unsubscribe hook; it exists for
// ChainSupervisor implementations outside this package (test fakes).
func NewHeadFeedSubscription(events <-chan HeadFeedEvent, unsubscribe func()) *HeadFeedSubscription {
	return &HeadFeedSubscription{Events: events, unsubscribe: unsubscribe}
}

// Unsubscribe removes the feed from its supervisor and closes Events. Idempotent.
func (s *HeadFeedSubscription) Unsubscribe() {
	s.unsubscribe()
}

// SubscribeHead returns a head feed computed over the upstreams that are
// Available and pass filter, re-evaluated on every event of every upstream. The
// initial event is the feed's current state: HeadUpdated when a member has a
// head, HeadFeedEmpty when no upstream matches, nothing when members exist but
// none has reported a head yet. name is used in logs only.
func (b *GenericChainSupervisor) SubscribeHead(name string, filter FilterUpstream) *HeadFeedSubscription {
	feed := &headFeed{
		name:    name,
		filter:  filter,
		fc:      b.newFc(),
		members: make(map[string]struct{}),
		events:  make(chan HeadFeedEvent, headFeedBuffer),
	}
	sub := NewHeadFeedSubscription(feed.events, func() { b.unsubscribeHead(feed) })

	b.feedsMu.Lock()
	defer b.feedsMu.Unlock()
	if b.feedsClosed {
		close(feed.events)
		return sub
	}

	var seed HeadFeedEvent
	b.upstreamStates.Range(func(id string, state *protocol.UpstreamState) bool {
		// during seeding the head only rises, so the last update is the owner
		if updated, head := feed.admit(id, state); updated && !head.IsEmptyByHeight() {
			seed = HeadUpdated{Head: head, UpstreamId: id}
		}
		return true
	})
	if len(feed.members) == 0 {
		seed = HeadFeedEmpty{}
	}
	if seed != nil {
		feed.events <- seed // the buffer is empty here, so this never blocks
	}
	b.feeds[feed] = struct{}{}
	return sub
}

// headFeedBuffer is the per-feed channel depth. Heads are low volume; a
// subscriber that falls this far behind has its feed closed (see updateFeeds).
const headFeedBuffer = 100

// headFeed is a head computed over the upstreams that pass filter: one fork
// choice over the members' heads, fed from the supervisor's event loop. All
// methods run under GenericChainSupervisor.feedsMu.
type headFeed struct {
	name    string
	filter  FilterUpstream
	fc      choice.ForkChoice
	members map[string]struct{}
	events  chan HeadFeedEvent
}

// apply re-evaluates upstreamId after one of its events and publishes what
// changed: HeadUpdated when the feed's head moved to a non-empty block,
// HeadFeedEmpty when the last member left. A nil state means the upstream is
// gone. It reports false when the subscriber is not reading (see publish).
func (f *headFeed) apply(upstreamId string, state *protocol.UpstreamState) bool {
	hadMembers := len(f.members) > 0
	updated, head := f.admit(upstreamId, state)
	delivered := true
	if updated && !head.IsEmptyByHeight() {
		delivered = f.publish(HeadUpdated{Head: head, UpstreamId: upstreamId})
	}
	if hadMembers && len(f.members) == 0 {
		delivered = f.publish(HeadFeedEmpty{}) && delivered
	}
	return delivered
}

// admit updates membership for upstreamId and feeds the fork choice: an
// Available upstream passing the filter is a member and its head counts; a
// member that stops passing is removed from both. The fork choice only ever
// tracks members, so an upstream that is not a member and does not pass needs
// nothing done. Returns the fork choice's verdict.
func (f *headFeed) admit(upstreamId string, state *protocol.UpstreamState) (bool, protocol.Block) {
	if state != nil && state.Status == protocol.Available && f.filter(upstreamId, state) {
		f.members[upstreamId] = struct{}{}
		return f.fc.Choose(upstreamId, &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: state.HeadData})
	}
	if _, member := f.members[upstreamId]; !member {
		return false, protocol.Block{}
	}
	delete(f.members, upstreamId)
	return f.fc.Choose(upstreamId, &protocol.HeadUpstreamEvent{Status: protocol.Unavailable})
}

// publish hands event to the subscriber without blocking the event loop and
// reports whether it fit. A full buffer means the subscriber is a whole buffer
// behind: nothing is dropped, the caller closes the feed instead, so the
// consumer takes its terminal path rather than silently missing an event.
func (f *headFeed) publish(event HeadFeedEvent) bool {
	select {
	case f.events <- event:
		return true
	default:
		return false
	}
}

// updateFeeds re-evaluates upstreamId against every feed after its event has
// been applied to upstreamStates. A feed whose subscriber is not reading is
// closed, like the subscription engine disconnects a too-slow client. Called
// from the event loop only.
func (b *GenericChainSupervisor) updateFeeds(upstreamId string) {
	b.feedsMu.Lock()
	defer b.feedsMu.Unlock()
	if len(b.feeds) == 0 {
		return
	}
	state, _ := b.upstreamStates.Load(upstreamId)
	for feed := range b.feeds {
		if !feed.apply(upstreamId, state) {
			log.Warn().Msgf("head feed %s: subscriber is not reading, feed closed", feed.name)
			delete(b.feeds, feed)
			close(feed.events)
		}
	}
}

func (b *GenericChainSupervisor) unsubscribeHead(feed *headFeed) {
	b.feedsMu.Lock()
	defer b.feedsMu.Unlock()
	if _, ok := b.feeds[feed]; !ok {
		return
	}
	delete(b.feeds, feed)
	close(feed.events)
}

// closeFeeds ends every feed when the supervisor stops; later SubscribeHead
// calls return an already-closed subscription.
func (b *GenericChainSupervisor) closeFeeds() {
	b.feedsMu.Lock()
	defer b.feedsMu.Unlock()
	b.feedsClosed = true
	for feed := range b.feeds {
		delete(b.feeds, feed)
		close(feed.events)
	}
}
