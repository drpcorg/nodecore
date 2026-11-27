package emerald_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The throttle tests run on real time. The interval is wide and every bound
// leaves a tenth of it as slack, so a loaded machine does not fail them.
const throttleInterval = 200 * time.Millisecond

func atLeast(interval time.Duration) time.Duration {
	return interval * 9 / 10
}

// throttledStream starts a stream over one announced upstream, up1 with index
// 00001 at revision 5, and waits for its Full.
func throttledStream(t *testing.T, minInterval, resyncInterval time.Duration) (*fakeUpstreamFeedSupervisor, *subscribeUpstreamStatusStream) {
	t.Helper()
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, minInterval, resyncInterval)
	waitForResponses(t, stream, 1)
	require.True(t, stream.ResponseAt(0).GetFullResponse())
	return feed, stream
}

func TestUpstreamStatusThrottle_FirstSendIsImmediate(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	// far longer than the bound below, so a slow machine cannot blur the two
	const interval = 5 * time.Second
	started := time.Now()

	startUpstreamStatus(t, upstreamSupervisor, stream, interval, longResync)

	waitForResponses(t, stream, 1)
	assert.Less(t, stream.ArrivalAt(0).Sub(started), time.Second)
}

func TestUpstreamStatusThrottle_MergesChangesInsideWindow(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)

	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	feed.Publish(headChange("up1", "00001", 7, protocol.NewBlockWithHeight(124)))

	waitForResponses(t, stream, 2)
	merged := stream.ResponseAt(1)
	assert.Equal(t, []string{"status", "head"}, eventKinds(merged))
	assert.False(t, merged.GetFullResponse())
	assert.Equal(t, uint64(124), headOf(merged).GetHeight())
	assert.GreaterOrEqual(t, stream.ArrivalAt(1).Sub(stream.ArrivalAt(0)), atLeast(throttleInterval))
	assertNoMoreResponses(t, stream, 2, throttleInterval*3/2)
}

func TestUpstreamStatusThrottle_LaterChangeOfSameKindWins(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)

	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	feed.Publish(headChange("up1", "00001", 7, protocol.NewBlockWithHeight(124)))
	feed.Publish(statusChange("up1", "00001", 8, protocol.Syncing))
	feed.Publish(headChange("up1", "00001", 9, protocol.NewBlockWithHeight(125)))

	waitForResponses(t, stream, 2)
	merged := stream.ResponseAt(1)
	assert.Equal(t, []string{"status", "head"}, eventKinds(merged))
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, statusOf(merged).GetAvailability())
	assert.Equal(t, uint64(125), headOf(merged).GetHeight())
	assertNoMoreResponses(t, stream, 2, throttleInterval*3/2)
}

// The interval is counted from when a held update went out, not from when it
// arrived: two responses about one upstream are never closer than the interval.
func TestUpstreamStatusThrottle_SpacingCountsFromTheFlush(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)

	time.Sleep(throttleInterval / 4)
	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	waitForResponses(t, stream, 2)
	feed.Publish(statusChange("up1", "00001", 7, protocol.Available))

	waitForResponses(t, stream, 3)
	assert.GreaterOrEqual(t, stream.ArrivalAt(1).Sub(stream.ArrivalAt(0)), atLeast(throttleInterval))
	assert.GreaterOrEqual(t, stream.ArrivalAt(2).Sub(stream.ArrivalAt(1)), atLeast(throttleInterval))
}

func TestUpstreamStatusThrottle_IsPerUpstream(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	feed.SetView("up2", upstreamView("00002", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, throttleInterval, longResync)
	waitForResponses(t, stream, 2)
	time.Sleep(throttleInterval + throttleInterval/10)

	// up1 is sent at once and is inside its window from here on
	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	waitForResponses(t, stream, 3)
	feed.Publish(headChange("up1", "00001", 7, protocol.NewBlockWithHeight(124)))
	feed.Publish(statusChange("up2", "00002", 8, protocol.Unavailable))

	// the order is the proof: up2's update was published after up1's and
	// still arrives first, because up1 is held and up2 is not
	waitForResponses(t, stream, 5)
	quiet, held := stream.ResponseAt(3), stream.ResponseAt(4)
	assert.Equal(t, "up2", quiet.GetUpstreamId())
	assert.Equal(t, "up1", held.GetUpstreamId())
	assert.Equal(t, []string{"head"}, eventKinds(held))
	assert.GreaterOrEqual(t, stream.ArrivalAt(4).Sub(stream.ArrivalAt(2)), atLeast(throttleInterval))
}

func TestUpstreamStatusThrottle_RemovalThenNewInstanceWithHeadSendsOnlyFull(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)

	feed.DeleteView("up1", 6)
	feed.Publish(&upstreams.UpstreamStateEvent{UpstreamId: "up1", UpstreamIndex: "00001", Revision: 6, Removed: true})
	feed.SetView("up1", upstreamView("00007", protocol.NewBlockWithHeight(10), "eth_call"), 7)
	feed.Publish(statusChange("up1", "00007", 7, protocol.Available))

	waitForResponses(t, stream, 2)
	replacement := stream.ResponseAt(1)
	assert.True(t, replacement.GetFullResponse())
	assert.Equal(t, "00007", replacement.GetUpstreamIndex())
	assert.Equal(t, fullKinds, eventKinds(replacement))
	assert.GreaterOrEqual(t, stream.ArrivalAt(1).Sub(stream.ArrivalAt(0)), atLeast(throttleInterval))
	assertNoMoreResponses(t, stream, 2, throttleInterval*3/2)
}

func TestUpstreamStatusThrottle_RemovalStaysWhenNewInstanceHasNoHead(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)

	feed.DeleteView("up1", 6)
	feed.Publish(&upstreams.UpstreamStateEvent{UpstreamId: "up1", UpstreamIndex: "00001", Revision: 6, Removed: true})
	feed.SetView("up1", upstreamView("00007", protocol.Block{}, "eth_call"), 7)
	feed.Publish(statusChange("up1", "00007", 7, protocol.Available))

	waitForResponses(t, stream, 2)
	removal := stream.ResponseAt(1)
	assert.Equal(t, []string{"status"}, eventKinds(removal))
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusOf(removal).GetAvailability())
	assert.Equal(t, "00001", removal.GetUpstreamIndex())

	head := protocol.NewBlockWithHeight(10)
	feed.SetView("up1", upstreamView("00007", head, "eth_call"), 8)
	feed.Publish(headChange("up1", "00007", 8, head))

	waitForResponses(t, stream, 3)
	assert.True(t, stream.ResponseAt(2).GetFullResponse())
	assert.Equal(t, "00007", stream.ResponseAt(2).GetUpstreamIndex())
}

// Resyncs that arrive while an update is held replace what is held, but never
// when it is due.
func TestUpstreamStatusThrottle_ResyncDoesNotPostponeHeldUpdate(t *testing.T) {
	const interval = 400 * time.Millisecond
	feed, stream := throttledStream(t, interval, testResyncInterval)

	feed.SetView("up1", withStatus(upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), protocol.Unavailable), 6)
	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))

	waitForResponses(t, stream, 2)
	// a resync that postponed the update would do so on every tick and the
	// update would never go out, so a loose upper bound proves the point
	waited := stream.ArrivalAt(1).Sub(stream.ArrivalAt(0))
	assert.GreaterOrEqual(t, waited, atLeast(interval))
	assert.Less(t, waited, interval*3)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusOf(stream.ResponseAt(1)).GetAvailability())
	assert.Nil(t, headOf(stream.ResponseAt(1)))
}

func TestUpstreamStatusThrottle_ZeroIntervalSendsEachEvent(t *testing.T) {
	feed, stream := throttledStream(t, 0, longResync)

	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	feed.Publish(headChange("up1", "00001", 7, protocol.NewBlockWithHeight(124)))

	waitForResponses(t, stream, 3)
	assert.Equal(t, []string{"status"}, eventKinds(stream.ResponseAt(1)))
	assert.Equal(t, []string{"head"}, eventKinds(stream.ResponseAt(2)))
}

func TestUpstreamStatusThrottle_TwoStreamsAreIndependent(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	immediate, throttled := newSubscribeUpstreamStatusStream(), newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, immediate, 0, longResync)
	startUpstreamStatus(t, upstreamSupervisor, throttled, throttleInterval, longResync)
	waitForResponses(t, immediate, 1)
	waitForResponses(t, throttled, 1)
	assert.True(t, immediate.ResponseAt(0).GetFullResponse())
	assert.True(t, throttled.ResponseAt(0).GetFullResponse())

	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))
	feed.Publish(headChange("up1", "00001", 7, protocol.NewBlockWithHeight(124)))

	waitForResponses(t, immediate, 3)
	waitForResponses(t, throttled, 2)
	assert.Equal(t, []string{"status"}, eventKinds(immediate.ResponseAt(1)))
	assert.Equal(t, []string{"head"}, eventKinds(immediate.ResponseAt(2)))
	assert.Equal(t, []string{"status", "head"}, eventKinds(throttled.ResponseAt(1)))
	assertNoMoreResponses(t, throttled, 2, throttleInterval*3/2)
}

// The same instance left and came back inside one window, so a Full is held
// for it. A change that follows must not be lost behind that Full.
func TestUpstreamStatusThrottle_ChangeAfterHeldFullIsNotLost(t *testing.T) {
	feed, stream := throttledStream(t, throttleInterval, longResync)
	head := protocol.NewBlockWithHeight(123)

	feed.DeleteView("up1", 6)
	feed.Publish(&upstreams.UpstreamStateEvent{UpstreamId: "up1", UpstreamIndex: "00001", Revision: 6, Removed: true})
	feed.SetView("up1", upstreamView("00001", head, "eth_call"), 7)
	feed.Publish(statusChange("up1", "00001", 7, protocol.Available))
	// let the stream read the returned instance before it changes again
	time.Sleep(throttleInterval / 4)
	feed.SetView("up1", withStatus(upstreamView("00001", head, "eth_call"), protocol.Syncing), 8)
	feed.Publish(statusChange("up1", "00001", 8, protocol.Syncing))

	waitForResponses(t, stream, 2)
	back := stream.ResponseAt(1)
	assert.True(t, back.GetFullResponse())
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, statusOf(back).GetAvailability())
	assertNoMoreResponses(t, stream, 2, throttleInterval*3/2)
}
