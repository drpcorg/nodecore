package utils_test

import (
	"sync"
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
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

func TestDuplicateSubscriptionPanics(t *testing.T) {
	sm := utils.NewSubscriptionManager[int]("test")
	sm.Subscribe("dup")

	assert.Panics(t, func() { sm.Subscribe("dup") })
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
