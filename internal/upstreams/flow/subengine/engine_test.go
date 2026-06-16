package subengine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(delay time.Duration) *baseEngine {
	return &baseEngine{
		ctx:           context.Background(),
		chain:         chains.ETHEREUM,
		teardownDelay: delay,
		sources:       utils.NewCMap[string, *sourceActor](),
	}
}

// Two subscribers sharing a key reuse one source (build called once) and both
// receive the same fanned-out events.
func TestEngineSharesSourceAcrossSubscribers(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	events := make(chan *protocol.WsResponse, 8)
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return &Source{Events: events, Stop: func() {}}, nil
	}

	sub1, err := e.Subscribe("k", build)
	require.NoError(t, err)
	sub2, err := e.Subscribe("k", build)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&builds))

	events <- &protocol.WsResponse{SubId: "x", Message: []byte("e")}
	assert.Equal(t, []byte("e"), (<-sub1.Events).Message)
	assert.Equal(t, []byte("e"), (<-sub2.Events).Message)

	sub1.Unsubscribe()
	sub2.Unsubscribe()
}

// The source must not consume events before the first subscriber attaches: an
// event already queued by the source at build time (e.g. the newHeads seed) is
// delivered to the first subscriber, not dropped to an empty subscriber set.
func TestEngineDoesNotDropEventsBeforeFirstSubscriber(t *testing.T) {
	e := newTestEngine(time.Minute)
	events := make(chan *protocol.WsResponse, 4)
	events <- &protocol.WsResponse{Message: []byte("seed")} // queued before anyone subscribes
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: events, Stop: func() {}}, nil
	}

	sub, err := e.Subscribe("k", build)
	require.NoError(t, err)

	select {
	case got := <-sub.Events:
		assert.Equal(t, []byte("seed"), got.Message)
	case <-time.After(time.Second):
		t.Fatal("first subscriber missed an event the source had already queued")
	}
}

func TestEngineDistinctKeysBuildSeparately(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return &Source{Events: make(chan *protocol.WsResponse), Stop: func() {}}, nil
	}

	_, err := e.Subscribe("k1", build)
	require.NoError(t, err)
	_, err = e.Subscribe("k2", build)
	require.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&builds))
}

// A build failure is returned to the caller and never retried into a spin.
func TestEngineBuildErrorIsReturnedNotRetried(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	_, err := e.Subscribe("k", func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return nil, assert.AnError
	})
	assert.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, int32(1), atomic.LoadInt32(&builds), "build must run exactly once")

	// A failed build must not leave an actor behind.
	require.Eventually(t, func() bool {
		_, ok := e.sources.Load("k")
		return !ok
	}, time.Second, 5*time.Millisecond)
}

// After the last subscriber leaves the source is torn down once teardownDelay
// elapses.
func TestEngineTeardownAfterLastUnsub(t *testing.T) {
	e := newTestEngine(30 * time.Millisecond)
	stopped := make(chan struct{})
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: make(chan *protocol.WsResponse), Stop: func() { close(stopped) }}, nil
	}

	sub, err := e.Subscribe("k", build)
	require.NoError(t, err)
	sub.Unsubscribe()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("source was not torn down after the last subscriber left")
	}
}

// Events arriving after the last subscriber leaves must not keep re-arming the
// teardown timer: a source on an active chain (event interval < teardownDelay)
// must still tear down once the grace window elapses.
func TestEngineTeardownDespiteOngoingEvents(t *testing.T) {
	e := newTestEngine(50 * time.Millisecond)
	events := make(chan *protocol.WsResponse)
	stopped := make(chan struct{})
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: events, Stop: func() { close(stopped) }}, nil
	}

	sub, err := e.Subscribe("k", build)
	require.NoError(t, err)
	sub.Unsubscribe()

	// Keep emitting faster than teardownDelay; these have no audience.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case events <- &protocol.WsResponse{Message: []byte("e")}:
			case <-stopped:
				return
			case <-time.After(time.Second):
				return
			}
		}
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not tear down while events kept flowing after the last unsub")
	}
	<-done
}

// A resubscribe within the teardown window reuses the source rather than
// rebuilding it.
func TestEngineResubscribeWithinWindowReusesSource(t *testing.T) {
	e := newTestEngine(time.Second)
	var builds int32
	events := make(chan *protocol.WsResponse, 8)
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return &Source{Events: events, Stop: func() {}}, nil
	}

	sub1, err := e.Subscribe("k", build)
	require.NoError(t, err)
	sub1.Unsubscribe()
	sub2, err := e.Subscribe("k", build)
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	assert.Equal(t, int32(1), atomic.LoadInt32(&builds))
}

// A terminal frame from the source closes every subscriber channel (out of
// band), exposes the cause via Err, removes the source, and lets the next
// Subscribe rebuild it.
func TestEngineTerminalClosesAndRemovesSource(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	built := make(chan chan *protocol.WsResponse, 4)
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		ev := make(chan *protocol.WsResponse, 4)
		built <- ev
		return &Source{Events: ev, Stop: func() {}}, nil
	}

	sub1, err := e.Subscribe("k", build)
	require.NoError(t, err)
	ev1 := <-built

	cause := &protocol.ResponseError{Code: 42, Message: "node said no"}
	ev1 <- &protocol.WsResponse{Error: cause}

	_, ok := <-sub1.Events // closed, not an in-band frame
	require.False(t, ok)
	require.Equal(t, cause, sub1.Err())

	require.Eventually(t, func() bool {
		_, ok := e.sources.Load("k")
		return !ok
	}, time.Second, 5*time.Millisecond)

	_, err = e.Subscribe("k", build)
	require.NoError(t, err)
	select {
	case <-built:
		assert.Equal(t, int32(2), atomic.LoadInt32(&builds))
	case <-time.After(time.Second):
		t.Fatal("source was not rebuilt after a terminal error")
	}
}

// A terminal must never be lost to a full buffer: a subscriber that never drains
// is disconnected with a terminal error instead of silently dropping events.
func TestEngineSlowConsumerDisconnectedDespiteFullBuffer(t *testing.T) {
	e := newTestEngine(time.Minute)
	events := make(chan *protocol.WsResponse)
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: events, Stop: func() {}}, nil
	}
	sub, err := e.Subscribe("k", build)
	require.NoError(t, err)

	for i := 0; i < subscriberBufferSize+5; i++ {
		events <- &protocol.WsResponse{Message: []byte("e")}
	}

	require.Eventually(t, func() bool { return sub.Err() != nil }, time.Second, 5*time.Millisecond)
	for range sub.Events { // drain to the close
	}
	assert.Equal(t, protocol.WsSubscriberTooSlowError().Code, sub.Err().Code)
}

// A fast subscriber keeps receiving even when a peer on the same source is
// disconnected for lagging.
func TestEngineSlowConsumerDoesNotAffectFastPeer(t *testing.T) {
	e := newTestEngine(time.Minute)
	events := make(chan *protocol.WsResponse)
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: events, Stop: func() {}}, nil
	}
	slow, err := e.Subscribe("k", build)
	require.NoError(t, err)
	fast, err := e.Subscribe("k", build)
	require.NoError(t, err)

	var got int64
	go func() {
		for range fast.Events {
			atomic.AddInt64(&got, 1)
		}
	}()

	for i := 0; i < subscriberBufferSize*3; i++ {
		events <- &protocol.WsResponse{Message: []byte("e")}
	}

	require.Eventually(t, func() bool { return slow.Err() != nil }, time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return atomic.LoadInt64(&got) > int64(subscriberBufferSize) }, time.Second, 5*time.Millisecond)
	fast.Unsubscribe()
}

// A slow build for one key does not block subscribes for other keys (each key
// has its own goroutine; no shared lock spans build).
func TestEngineBuildDoesNotBlockOtherKeys(t *testing.T) {
	e := newTestEngine(time.Minute)
	release := make(chan struct{})
	slowBuild := func(_ context.Context) (*Source, error) {
		<-release
		return &Source{Events: make(chan *protocol.WsResponse), Stop: func() {}}, nil
	}
	fastBuild := func(_ context.Context) (*Source, error) {
		return &Source{Events: make(chan *protocol.WsResponse), Stop: func() {}}, nil
	}

	go func() { _, _ = e.Subscribe("slow", slowBuild) }()

	done := make(chan error, 1)
	go func() {
		_, err := e.Subscribe("fast", fastBuild)
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("subscribe for an unrelated key blocked on an in-flight build")
	}
	close(release)
}
