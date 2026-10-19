package upstreams

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedForkChoice records every Choose call and answers with a fixed verdict.
type scriptedForkChoice struct {
	events  []*protocol.HeadUpstreamEvent
	ids     []string
	updated bool
	head    protocol.Block
}

func (s *scriptedForkChoice) Choose(upstreamId string, event *protocol.HeadUpstreamEvent) (bool, protocol.Block) {
	s.ids = append(s.ids, upstreamId)
	s.events = append(s.events, event)
	return s.updated, s.head
}

func newTestFeed(fc *scriptedForkChoice, filter FilterUpstream, buffer int) *headFeed {
	return &headFeed{
		name:    "test",
		filter:  filter,
		fc:      fc,
		members: make(map[string]struct{}),
		events:  make(chan HeadFeedEvent, buffer),
	}
}

func availableState(height uint64) *protocol.UpstreamState {
	return &protocol.UpstreamState{Status: protocol.Available, HeadData: protocol.NewBlockWithHeight(height)}
}

func acceptAll(string, *protocol.UpstreamState) bool { return true }
func rejectAll(string, *protocol.UpstreamState) bool { return false }

func TestAdmitPassingUpstreamBecomesMemberAndFeedsForkChoice(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 1)

	updated, head := feed.admit("up1", availableState(10))

	assert.True(t, updated)
	assert.Equal(t, uint64(10), head.Height)
	assert.Contains(t, feed.members, "up1")
	require.Len(t, fc.events, 1)
	assert.Equal(t, protocol.Available, fc.events[0].Status)
	assert.Equal(t, uint64(10), fc.events[0].Head.Height)
}

func TestAdmitNonMemberThatFailsTouchesNothing(t *testing.T) {
	fc := &scriptedForkChoice{}
	feed := newTestFeed(fc, rejectAll, 1)

	updated, head := feed.admit("up1", availableState(10))

	assert.False(t, updated)
	assert.True(t, head.IsEmptyByHeight())
	assert.Empty(t, feed.members)
	assert.Empty(t, fc.events, "the fork choice never tracked up1, so it must not be asked")
}

func TestAdmitUnavailableStatusIsNotAdmittedEvenIfFilterPasses(t *testing.T) {
	fc := &scriptedForkChoice{}
	feed := newTestFeed(fc, acceptAll, 1)

	feed.admit("up1", &protocol.UpstreamState{Status: protocol.Unavailable, HeadData: protocol.NewBlockWithHeight(10)})

	assert.Empty(t, feed.members)
	assert.Empty(t, fc.events)
}

func TestAdmitMemberThatStopsPassingIsRemovedFromBoth(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	passes := true
	feed := newTestFeed(fc, func(string, *protocol.UpstreamState) bool { return passes }, 1)
	feed.admit("up1", availableState(10))

	passes = false
	feed.admit("up1", availableState(11))

	assert.NotContains(t, feed.members, "up1")
	require.Len(t, fc.events, 2)
	assert.Equal(t, protocol.Unavailable, fc.events[1].Status)
}

func TestAdmitNilStateRemovesMember(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 1)
	feed.admit("up1", availableState(10))

	feed.admit("up1", nil) // RemoveUpstreamEvent: the state is gone

	assert.Empty(t, feed.members)
	require.Len(t, fc.events, 2)
	assert.Equal(t, protocol.Unavailable, fc.events[1].Status)
}

func TestApplyPublishesHeadUpdatedWithTriggeringUpstream(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 1)

	require.True(t, feed.apply("up1", availableState(10)))

	event := <-feed.events
	updated, ok := event.(HeadUpdated)
	require.True(t, ok)
	assert.Equal(t, uint64(10), updated.Head.Height)
	assert.Equal(t, "up1", updated.UpstreamId)
}

func TestApplyPublishesNothingWhenForkChoiceIsUnchanged(t *testing.T) {
	fc := &scriptedForkChoice{updated: false, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 1)

	require.True(t, feed.apply("up1", availableState(5)))

	assert.Empty(t, feed.events)
}

func TestApplyPublishesEmptyWhenLastMemberLeaves(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 2)
	feed.apply("up1", availableState(10))
	<-feed.events

	fc.updated, fc.head = true, protocol.Block{} // the max is gone with the member
	require.True(t, feed.apply("up1", nil))

	assert.IsType(t, HeadFeedEmpty{}, <-feed.events)
	assert.Empty(t, feed.events, "an empty head is not a HeadUpdated")
}

func TestApplyPublishesNothingWhenHeadEmptiesButMembersRemain(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 2)
	feed.apply("up1", availableState(10))
	<-feed.events
	fc.updated = false
	feed.apply("up2", availableState(0)) // member without a head
	require.Len(t, feed.members, 2)

	fc.updated, fc.head = true, protocol.Block{}
	require.True(t, feed.apply("up1", nil))

	assert.Empty(t, feed.events, "up2 is still a member: not empty, nothing new to say")
}

func TestApplyReportsFalseWhenSubscriberIsNotReading(t *testing.T) {
	fc := &scriptedForkChoice{updated: true, head: protocol.NewBlockWithHeight(10)}
	feed := newTestFeed(fc, acceptAll, 1)

	require.True(t, feed.apply("up1", availableState(10))) // fills the single slot
	assert.False(t, feed.apply("up1", availableState(11))) // nowhere to put the next one
}

func TestPublishReportsWhetherEventFit(t *testing.T) {
	feed := newTestFeed(&scriptedForkChoice{}, acceptAll, 1)

	assert.True(t, feed.publish(HeadFeedEmpty{}))
	assert.False(t, feed.publish(HeadFeedEmpty{}))
	assert.Len(t, feed.events, 1, "a non-fitting event is not queued")
}
