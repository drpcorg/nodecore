package subengine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(delay time.Duration) *baseEngine {
	return &baseEngine{
		ctx:           context.Background(),
		chain:         chains.ETHEREUM,
		teardownDelay: delay,
		sources:       make(map[string]*sharedSource),
	}
}

// Two subscribers sharing a key must reuse one source (build called once) and
// both receive the same fanned-out events.
func TestEngineSharesSourceAcrossSubscribers(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	events := make(chan *protocol.WsResponse, 8)
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return &Source{Events: events, Stop: func() {}}, nil
	}

	ch1, unsub1, err := e.Subscribe("k", build)
	require.NoError(t, err)
	ch2, unsub2, err := e.Subscribe("k", build)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&builds))

	// the shared event fans out to every subscriber
	events <- &protocol.WsResponse{SubId: "x", Message: []byte("e")}
	assert.Equal(t, []byte("e"), (<-ch1).Message)
	assert.Equal(t, []byte("e"), (<-ch2).Message)

	unsub1()
	unsub2()
}

func TestEngineDistinctKeysBuildSeparately(t *testing.T) {
	e := newTestEngine(time.Minute)
	var builds int32
	build := func(_ context.Context) (*Source, error) {
		atomic.AddInt32(&builds, 1)
		return &Source{Events: make(chan *protocol.WsResponse), Stop: func() {}}, nil
	}

	_, _, err := e.Subscribe("k1", build)
	require.NoError(t, err)
	_, _, err = e.Subscribe("k2", build)
	require.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&builds))
}

func TestEngineBuildErrorIsReturned(t *testing.T) {
	e := newTestEngine(time.Minute)
	_, _, err := e.Subscribe("k", func(_ context.Context) (*Source, error) {
		return nil, assert.AnError
	})
	assert.ErrorIs(t, err, assert.AnError)
}

// After the last subscriber leaves the source is torn down once teardownDelay
// elapses.
func TestEngineTeardownAfterLastUnsub(t *testing.T) {
	e := newTestEngine(30 * time.Millisecond)
	stopped := make(chan struct{})
	events := make(chan *protocol.WsResponse)
	build := func(_ context.Context) (*Source, error) {
		return &Source{Events: events, Stop: func() { close(events); close(stopped) }}, nil
	}

	_, unsub, err := e.Subscribe("k", build)
	require.NoError(t, err)
	unsub()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("source was not torn down after the last subscriber left")
	}
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

	_, unsub1, err := e.Subscribe("k", build)
	require.NoError(t, err)
	unsub1()
	_, unsub2, err := e.Subscribe("k", build)
	require.NoError(t, err)
	defer unsub2()

	assert.Equal(t, int32(1), atomic.LoadInt32(&builds))
}

// A terminal frame from the source is fanned out to clients and removes the
// source so the next subscribe rebuilds it.
func TestEngineTerminalErrorRemovesSource(t *testing.T) {
	e := newTestEngine(time.Minute)
	built := make(chan chan *protocol.WsResponse, 4)
	build := func(_ context.Context) (*Source, error) {
		ev := make(chan *protocol.WsResponse, 4)
		built <- ev
		return &Source{Events: ev, Stop: func() {}}, nil
	}

	ch1, _, err := e.Subscribe("k", build)
	require.NoError(t, err)
	ev1 := <-built

	ev1 <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
	close(ev1)

	terminal := <-ch1
	require.NotNil(t, terminal.Error)

	require.Eventually(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		_, ok := e.sources["k"]
		return !ok
	}, time.Second, 5*time.Millisecond)

	_, _, err = e.Subscribe("k", build)
	require.NoError(t, err)
	select {
	case <-built: // a second build happened
	case <-time.After(time.Second):
		t.Fatal("source was not rebuilt after a terminal error")
	}
}
