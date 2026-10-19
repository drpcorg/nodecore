package upstreams_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newFeedSupervisor(ctx context.Context) *upstreams.GenericChainSupervisor {
	sup := upstreams.NewGenericChainSupervisor(ctx, chains.ARBITRUM, fork_choice.NewHeightForkChoice, nil, false, nil)
	go sup.Start()
	return sup
}

func feedBlock(height uint64, hash string) protocol.Block {
	return protocol.NewBlock(height, 0, blockchain.NewHashIdFromString(hash), blockchain.NewHashIdFromString("parent"))
}

// feedState is an upstream state with the given status, head and caps.
func feedState(status protocol.AvailabilityStatus, head protocol.Block, caps ...protocol.Cap) *protocol.UpstreamState {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]()).Maybe()
	methodsMock.On("HasMethod", mock.Anything).Return(false).Maybe()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](caps...), "", nil, nil)
	state.Status = status
	state.HeadData = head
	return &state
}

func publishState(sup *upstreams.GenericChainSupervisor, id string, state *protocol.UpstreamState) {
	sup.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.StateUpstreamEvent{State: state}})
}

func publishValid(sup *upstreams.GenericChainSupervisor, id string, state *protocol.UpstreamState) {
	sup.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.ValidUpstreamEvent{State: state}})
}

func publishHead(sup *upstreams.GenericChainSupervisor, id string, head protocol.Block) {
	sup.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: head}})
}

func readFeed(t *testing.T, sub *upstreams.HeadFeedSubscription) upstreams.HeadFeedEvent {
	t.Helper()
	select {
	case ev, ok := <-sub.Events:
		require.True(t, ok, "feed closed unexpectedly")
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a feed event")
		return nil
	}
}

func assertNoFeedEvent(t *testing.T, sub *upstreams.HeadFeedSubscription) {
	t.Helper()
	select {
	case ev, ok := <-sub.Events:
		t.Fatalf("unexpected feed event %#v (open=%v)", ev, ok)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertHeadUpdated(t *testing.T, ev upstreams.HeadFeedEvent, height uint64, upstreamId string) {
	t.Helper()
	updated, ok := ev.(upstreams.HeadUpdated)
	require.True(t, ok, "expected HeadUpdated, got %#v", ev)
	assert.Equal(t, height, updated.Head.Height)
	assert.Equal(t, upstreamId, updated.UpstreamId)
}

func hasCap(c protocol.Cap) upstreams.FilterUpstream {
	return func(_ string, state *protocol.UpstreamState) bool {
		return state.Caps != nil && state.Caps.Contains(c)
	}
}

func anyUpstream(string, *protocol.UpstreamState) bool { return true }

// waitForChainHead blocks until the global merged head reaches height, so a test
// knows the supervisor has processed everything published before.
func waitForChainHead(t *testing.T, sup *upstreams.GenericChainSupervisor, height uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		return sup.GetChainState().HeadData.Head.Height == height
	}, time.Second, 10*time.Millisecond)
}

func TestHeadFeedSeedsEmptyWhenNothingMatches(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream)
	defer sub.Unsubscribe()

	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))
}

func TestHeadFeedSeedsCurrentHead(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"), protocol.NewHeadsCap))
	publishState(sup, "up2", feedState(protocol.Available, feedBlock(90, "b"), protocol.NewHeadsCap))
	publishState(sup, "up3", feedState(protocol.Available, feedBlock(200, "c"))) // no cap
	publishHead(sup, "up3", feedBlock(200, "c"))
	waitForChainHead(t, sup, 200)

	sub := sup.SubscribeHead("t", hasCap(protocol.NewHeadsCap))
	defer sub.Unsubscribe()

	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")
	assertNoFeedEvent(t, sub)
}

func TestHeadFeedNoSeedWhenMembersHaveNoHead(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	publishState(sup, "up1", feedState(protocol.Available, protocol.Block{}, protocol.NewHeadsCap))
	require.Eventually(t, func() bool { return sup.GetUpstreamState("up1") != nil }, time.Second, 10*time.Millisecond)

	sub := sup.SubscribeHead("t", hasCap(protocol.NewHeadsCap))
	defer sub.Unsubscribe()

	assertNoFeedEvent(t, sub) // a member without a head is not "empty"
	publishHead(sup, "up1", feedBlock(5, "a"))
	assertHeadUpdated(t, readFeed(t, sub), 5, "up1")
}

func TestHeadFeedFollowsOnlyMatchingUpstreams(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", hasCap(protocol.NewHeadsCap))
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"))) // no cap
	publishHead(sup, "up1", feedBlock(100, "a"))
	assertNoFeedEvent(t, sub)

	publishState(sup, "up2", feedState(protocol.Available, feedBlock(50, "b"), protocol.NewHeadsCap))
	assertHeadUpdated(t, readFeed(t, sub), 50, "up2")

	publishHead(sup, "up1", feedBlock(200, "c")) // ahead, but not a member
	assertNoFeedEvent(t, sub)

	publishHead(sup, "up2", feedBlock(51, "d"))
	assertHeadUpdated(t, readFeed(t, sub), 51, "up2")
	assert.Equal(t, uint64(200), sup.GetChainState().HeadData.Head.Height, "the global head is unaffected")
}

func TestHeadFeedEvictsOnStatusChangeAndReadmits(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", hasCap(protocol.NewHeadsCap))
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"), protocol.NewHeadsCap))
	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")

	publishState(sup, "up1", feedState(protocol.Unavailable, feedBlock(100, "a"), protocol.NewHeadsCap))
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishValid(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"), protocol.NewHeadsCap))
	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")
}

func TestHeadFeedEvictsWhenFilterStopsMatching(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", hasCap(protocol.LogsCap))
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"), protocol.NewHeadsCap, protocol.LogsCap))
	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")

	// eth_getLogs banned: LogsCap gone, NewHeadsCap stays
	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a"), protocol.NewHeadsCap))
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))
}

func TestHeadFeedRemoveUpstream(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream)
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a")))
	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")
	publishState(sup, "up2", feedState(protocol.Available, feedBlock(90, "b")))
	assertNoFeedEvent(t, sub)

	sup.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up1"))
	// the head moves down to up2's; UpstreamId is the upstream whose event caused it
	assertHeadUpdated(t, readFeed(t, sub), 90, "up1")

	sup.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up2"))
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))
}

func TestHeadFeedMemberWithoutHeadKeepsFeedAlive(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream)
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a")))
	assertHeadUpdated(t, readFeed(t, sub), 100, "up1")
	publishState(sup, "up2", feedState(protocol.Available, protocol.Block{})) // member, no head yet

	sup.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up1"))
	assertNoFeedEvent(t, sub) // up2 is still a member: not empty, nothing new to say

	publishHead(sup, "up2", feedBlock(7, "b"))
	assertHeadUpdated(t, readFeed(t, sub), 7, "up2")
}

func TestHeadFeedHeightDependentFilter(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	atLeast150 := func(_ string, state *protocol.UpstreamState) bool { return state.HeadData.Height >= 150 }
	sub := sup.SubscribeHead("t", atLeast150)
	defer sub.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a")))
	assertNoFeedEvent(t, sub)

	publishHead(sup, "up1", feedBlock(150, "b")) // admitted on a head event alone
	assertHeadUpdated(t, readFeed(t, sub), 150, "up1")

	publishHead(sup, "up1", feedBlock(120, "c")) // rolled back below: evicted
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))
}

func TestHeadFeedTwoFeedsDifferentFilters(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	capped := sup.SubscribeHead("capped", hasCap(protocol.NewHeadsCap))
	defer capped.Unsubscribe()
	all := sup.SubscribeHead("all", anyUpstream)
	defer all.Unsubscribe()
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, capped))
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, all))

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a")))
	assertHeadUpdated(t, readFeed(t, all), 100, "up1")
	assertNoFeedEvent(t, capped)

	publishState(sup, "up2", feedState(protocol.Available, feedBlock(50, "b"), protocol.NewHeadsCap))
	assertHeadUpdated(t, readFeed(t, capped), 50, "up2")
	assertNoFeedEvent(t, all)
}

func TestHeadFeedUnsubscribeClosesAndIsIdempotent(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream)
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	sub.Unsubscribe()
	_, ok := <-sub.Events
	assert.False(t, ok, "Events must be closed after Unsubscribe")
	sub.Unsubscribe() // must not panic

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(100, "a")))
	publishHead(sup, "up1", feedBlock(100, "a"))
	waitForChainHead(t, sup, 100) // the loop survived applying events to a removed feed
}

func TestHeadFeedClosedOnContextEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sup := newFeedSupervisor(ctx)
	sub := sup.SubscribeHead("t", anyUpstream)
	assert.IsType(t, upstreams.HeadFeedEmpty{}, readFeed(t, sub))

	cancel()
	select {
	case _, ok := <-sub.Events:
		assert.False(t, ok, "Events must be closed when the supervisor stops")
	case <-time.After(time.Second):
		t.Fatal("feed not closed after context end")
	}

	late := sup.SubscribeHead("late", anyUpstream)
	_, ok := <-late.Events
	assert.False(t, ok, "a feed subscribed after shutdown is closed immediately")
}

func TestHeadFeedSlowSubscriberDoesNotBlockSupervisor(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream) // never read
	defer sub.Unsubscribe()

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(1, "a")))
	for h := uint64(2); h <= 300; h++ {
		publishHead(sup, "up1", feedBlock(h, "x"))
	}
	waitForChainHead(t, sup, 300) // the event loop kept going past a full feed buffer
}

// A subscriber that has fallen a full buffer behind is broken, not merely slow:
// its feed is closed (the engine does the same to a too-slow client) so the
// consumer takes its terminal path instead of silently missing events - in
// particular HeadFeedEmpty, which nothing follows.
func TestHeadFeedSlowSubscriberIsClosed(t *testing.T) {
	sup := newFeedSupervisor(context.Background())
	sub := sup.SubscribeHead("t", anyUpstream) // never read
	defer sub.Unsubscribe()

	publishState(sup, "up1", feedState(protocol.Available, feedBlock(1, "a")))
	for h := uint64(2); h <= 150; h++ { // well past the buffer depth
		publishHead(sup, "up1", feedBlock(h, "x"))
	}
	waitForChainHead(t, sup, 150)

	closed := false
	for !closed {
		select {
		case _, ok := <-sub.Events:
			closed = !ok
		default:
			t.Fatal("feed still open after overflowing its buffer")
		}
	}
}
