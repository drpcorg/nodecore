package utils_test

import (
	"sync"
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribeReceivesPublishedEvents(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sub := sm.Subscribe("s1")

	sm.Publish(1)
	sm.Publish(2)

	assert.Equal(t, 1, <-sub.Events)
	assert.Equal(t, 2, <-sub.Events)
}

func TestPublishFansOutToAllSubscribers(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sub1 := sm.Subscribe("s1")
	sub2 := sm.Subscribe("s2")

	sm.Publish(42)

	assert.Equal(t, 42, <-sub1.Events)
	assert.Equal(t, 42, <-sub2.Events)
}

func TestSubscribeDoesNotReplayPriorEvents(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sm.Publish(1) // published before anyone subscribed

	sub := sm.Subscribe("s1")

	// A plain Subscribe only sees events published after it registered.
	assert.Empty(t, drain(sub))

	sm.Publish(2)
	assert.Equal(t, 2, <-sub.Events)
}

func TestSubscribeWithReplaySeedsLastEvent(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sm.Publish(1)
	sm.Publish(2)

	sub := sm.SubscribeWithReplay("s1")

	// Replay seeds with the most recent event, not the whole history.
	assert.Equal(t, []int{2}, drain(sub))

	sm.Publish(3)
	assert.Equal(t, 3, <-sub.Events)
}

func TestSubscribeWithReplayNoPriorEvent(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")

	// Nothing has been published yet, so there is nothing to replay.
	sub := sm.SubscribeWithReplay("s1")
	assert.Empty(t, drain(sub))

	sm.Publish(7)
	assert.Equal(t, 7, <-sub.Events)
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sub := sm.Subscribe("s1")

	sub.Unsubscribe()

	// Channel is closed by Unsubscribe.
	_, ok := <-sub.Events
	assert.False(t, ok)

	// Publishing after Unsubscribe must not panic on the closed channel.
	assert.NotPanics(t, func() { sm.Publish(1) })
}

func TestPublishDropsWhenBufferFull(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sub := sm.SubscribeWithSize("s1", 1)

	// First fits the buffer; second is dropped (non-blocking send) rather than
	// blocking the publisher.
	assert.NotPanics(t, func() {
		sm.Publish(1)
		sm.Publish(2)
	})

	assert.Equal(t, []int{1}, drain(sub))
}

// A name is a label, not an identity: two subscribers may share one, both are served, and
// removing one leaves the other untouched. This is what lets a restarted consumer resubscribe
// under its static name while the previous run's subscription is still being released.
func TestSameNamedSubscribersAreBothServed(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	first := sm.Subscribe("dup")
	second := sm.Subscribe("dup")

	sm.Publish(1)
	assert.Equal(t, 1, <-first.Events)
	assert.Equal(t, 1, <-second.Events)

	first.Unsubscribe()
	sm.Publish(2)
	assert.Equal(t, 2, <-second.Events)
}

func TestUnsubscribeTwiceCountsOnce(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("unsubscribe_twice_manager")
	sub := sm.Subscribe("s1")
	assert.Equal(t, 1.0, gaugeValue(t, "chanutil_subscriptions_num", "unsubscribe_twice_manager"))

	sub.Unsubscribe()
	sub.Unsubscribe()

	assert.Equal(t, 0.0, gaugeValue(t, "chanutil_subscriptions_num", "unsubscribe_twice_manager"))
}

// The unread gauge is per subscriber name and reports the largest backlog among the
// subscribers sharing that name, so a slow consumer is visible by name even while a
// restarted twin with the same name is keeping up.
func TestUnreadMessagesIsMaxPerName(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("unread_max_manager")
	slowStuck := sm.SubscribeWithSize("slow", 5)
	slowDrained := sm.SubscribeWithSize("slow", 5)
	fast := sm.SubscribeWithSize("fast", 5)

	sm.Publish(1)
	sm.Publish(2)
	sm.Publish(3)
	drain(slowDrained)
	drain(fast)
	// the gauge is sampled before each delivery: slowStuck still holds 3, the others 0
	sm.Publish(4)

	assert.Equal(t, 3.0, gaugeValue(t, "chanutil_unread_messages_num", "slow"))
	assert.Equal(t, 0.0, gaugeValue(t, "chanutil_unread_messages_num", "fast"))
	_ = slowStuck
}

// TestSubscribeWithReplayAtomicVsPublish exercises the TOCTOU window the manager
// lock closes: a subscriber that snapshots the last state and then registers must
// never miss a transition that happens in between. We publish an initial state,
// then race a single state-change publish against a replay-subscribe, and assert
// the subscriber's observed sequence always ends on the final published state.
// Run with -race; without the lock this fails intermittently (the subscriber gets
// stuck on the stale initial state).
func TestSubscribeWithReplayAtomicVsPublish(t *testing.T) {
	for i := 0; i < 2000; i++ {
		sm := utils.NewSubscriptionManager[bool]("test")
		sm.Publish(true) // initial "connected" state

		var sub *utils.Subscription[bool]
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			sm.Publish(false) // "disconnect"
		}()
		go func() {
			defer wg.Done()
			sub = sm.SubscribeWithReplay("s1")
		}()

		wg.Wait()

		got := drain(sub)
		require.NotEmptyf(t, got, "iteration %d: subscriber observed no state", i)
		// Whether it replayed [false], replayed [true] then received [false], or
		// replayed [false] directly, the last observed state must be the final one.
		assert.Falsef(t, got[len(got)-1], "iteration %d: subscriber stuck on stale state, saw %v", i, got)
	}
}

// gaugeValue reads a registered gauge by metric name and source label from the default
// registry; the manager's collectors are package-private, so this is the public view.
func gaugeValue(t *testing.T, metric, source string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != metric {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "source" && label.GetValue() == source {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatalf("no gauge %s{source=%q}", metric, source)
	return 0
}

// drain returns all currently-buffered events without blocking.
func drain[T any](sub *utils.Subscription[T]) []T {
	var out []T
	for {
		select {
		case v := <-sub.Events:
			out = append(out, v)
		default:
			return out
		}
	}
}
