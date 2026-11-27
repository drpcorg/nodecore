package upstreams_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var everyUpstreamKind = []string{
	"*upstreams.StatusWrapper",
	"*upstreams.MethodsWrapper",
	"*upstreams.LowerBoundsWrapper",
	"*upstreams.HeadWrapper",
	"*upstreams.BlocksWrapper",
	"*upstreams.SubMethodsWrapper",
	"*upstreams.LabelsWrapper",
}

func readUpstreamStateEvent(t *testing.T, sub *utils.Subscription[*upstreams.UpstreamStateEvent]) *upstreams.UpstreamStateEvent {
	t.Helper()
	select {
	case event, ok := <-sub.Events:
		require.True(t, ok, "subscription closed unexpectedly")
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an upstream state event")
		return nil
	}
}

func assertNoUpstreamStateEvent(t *testing.T, sub *utils.Subscription[*upstreams.UpstreamStateEvent]) {
	t.Helper()
	select {
	case event := <-sub.Events:
		t.Fatalf("unexpected upstream state event %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func wrapperKinds(wrappers []upstreams.UpstreamStateWrapper) []string {
	names := make([]string, 0, len(wrappers))
	for _, wrapper := range wrappers {
		names = append(names, fmt.Sprintf("%T", wrapper))
	}
	return names
}

func publishRemove(sup *upstreams.GenericChainSupervisor, id string) {
	sup.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.RemoveUpstreamEvent{}})
}

func indexedState(index string, status protocol.AvailabilityStatus, head protocol.Block, caps ...protocol.Cap) *protocol.UpstreamState {
	state := feedState(status, head, caps...)
	state.UpstreamIndex = index
	return state
}

func TestUpstreamFeedNewUpstreamPublishesEveryKind(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")

	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))

	event := readUpstreamStateEvent(t, sub)
	assert.False(t, event.Removed)
	assert.Equal(t, "up1", event.UpstreamId)
	assert.Equal(t, "00001", event.UpstreamIndex)
	assert.Equal(t, everyUpstreamKind, wrapperKinds(event.Changes))
}

func TestUpstreamFeedHeadEventPublishesOnlyHead(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(9, "a")))
	readUpstreamStateEvent(t, sub)

	publishHead(sup, "up1", feedBlock(10, "b"))

	event := readUpstreamStateEvent(t, sub)
	require.Len(t, event.Changes, 1)
	head, ok := event.Changes[0].(*upstreams.HeadWrapper)
	require.True(t, ok)
	assert.Equal(t, uint64(10), head.Head.Height)
	assert.Equal(t, "00001", event.UpstreamIndex)

	// the same head again is not a change
	publishHead(sup, "up1", feedBlock(10, "b"))
	assertNoUpstreamStateEvent(t, sub)
}

func TestUpstreamFeedStatePublishesOnlyChangedKinds(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	readUpstreamStateEvent(t, sub)

	publishState(sup, "up1", indexedState("00001", protocol.Unavailable, feedBlock(10, "a")))

	event := readUpstreamStateEvent(t, sub)
	require.Len(t, event.Changes, 1)
	status, ok := event.Changes[0].(*upstreams.StatusWrapper)
	require.True(t, ok)
	assert.Equal(t, protocol.Unavailable, status.Status)
}

func TestUpstreamFeedUnchangedStatePublishesNothingButAdvancesRevision(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	first := readUpstreamStateEvent(t, sub)

	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))

	assertNoUpstreamStateEvent(t, sub)
	assert.Equal(t, first.Revision+1, sup.GetUpstreamViews().Revision)
}

func TestUpstreamFeedRemoval(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	added := readUpstreamStateEvent(t, sub)

	publishRemove(sup, "up1")

	removed := readUpstreamStateEvent(t, sub)
	assert.True(t, removed.Removed)
	assert.Equal(t, "up1", removed.UpstreamId)
	assert.Equal(t, "00001", removed.UpstreamIndex)
	assert.Empty(t, removed.Changes)
	assert.Greater(t, removed.Revision, added.Revision)
	_, _, known := sup.GetUpstreamView("up1")
	assert.False(t, known)

	publishRemove(sup, "up1")
	assertNoUpstreamStateEvent(t, sub)
}

func TestUpstreamFeedValidAfterRemovalPublishesEveryKind(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	readUpstreamStateEvent(t, sub)
	publishRemove(sup, "up1")
	removed := readUpstreamStateEvent(t, sub)

	publishValid(sup, "up1", indexedState("00001", protocol.Available, feedBlock(11, "b")))

	event := readUpstreamStateEvent(t, sub)
	assert.False(t, event.Removed)
	assert.Equal(t, everyUpstreamKind, wrapperKinds(event.Changes))
	assert.Greater(t, event.Revision, removed.Revision)
}

// Real upstream methods take the shortcut "same object, same methods"; a new
// object is compared by content.
func TestUpstreamFeedMethodsChange(t *testing.T) {
	unchanged := mustNewUpstreamMethods(t, nil)
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	stateWith := func(status protocol.AvailabilityStatus, upstreamMethods methods.Methods) *protocol.UpstreamState {
		state := indexedState("00001", status, feedBlock(10, "a"))
		state.UpstreamMethods = upstreamMethods
		return state
	}
	publishState(sup, "up1", stateWith(protocol.Available, unchanged))
	readUpstreamStateEvent(t, sub)

	publishState(sup, "up1", stateWith(protocol.Unavailable, unchanged))
	assert.Equal(t, []string{"*upstreams.StatusWrapper"}, wrapperKinds(readUpstreamStateEvent(t, sub).Changes))

	publishState(sup, "up1", stateWith(protocol.Unavailable, mustNewUpstreamMethods(t, nil)))
	assertNoUpstreamStateEvent(t, sub)

	withoutCall := mustNewUpstreamMethods(t, &config.MethodsConfig{DisableMethods: []string{"eth_call"}})
	publishState(sup, "up1", stateWith(protocol.Unavailable, withoutCall))
	event := readUpstreamStateEvent(t, sub)
	require.Equal(t, []string{"*upstreams.MethodsWrapper"}, wrapperKinds(event.Changes))
	changed := event.Changes[0].(*upstreams.MethodsWrapper).Methods
	assert.NotContains(t, changed, "eth_call")
	assert.Contains(t, changed, "eth_getBalance")
}

// a caps change reaches the stream only as a subscription-methods change
func TestUpstreamFeedNeverPublishesCapsWrapper(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")

	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a"), protocol.WsCap, protocol.NewHeadsCap))
	publishState(sup, "up1", indexedState("00001", protocol.Unavailable, feedBlock(10, "a"), protocol.WsCap))

	first := readUpstreamStateEvent(t, sub)
	assert.NotContains(t, wrapperKinds(first.Changes), "*upstreams.CapsWrapper")
	// the caps-only change published nothing: the next event is the status one
	second := readUpstreamStateEvent(t, sub)
	assert.Equal(t, []string{"*upstreams.StatusWrapper"}, wrapperKinds(second.Changes))
	assertNoUpstreamStateEvent(t, sub)
}

func TestUpstreamFeedEventRevisionsIncrease(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")

	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(10, "a")))
	publishHead(sup, "up1", feedBlock(11, "b"))
	publishState(sup, "up2", indexedState("00002", protocol.Available, feedBlock(11, "b")))

	first, second, third := readUpstreamStateEvent(t, sub), readUpstreamStateEvent(t, sub), readUpstreamStateEvent(t, sub)
	assert.Less(t, first.Revision, second.Revision)
	assert.Less(t, second.Revision, third.Revision)
	assert.Equal(t, third.Revision, sup.GetUpstreamViews().Revision)
}

// A read's revision must describe its content exactly: everything up to that
// revision is in it and nothing after it.
func TestUpstreamFeedReadsMatchTheirRevision(t *testing.T) {
	const heads = 50
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	publishState(sup, "up1", indexedState("00001", protocol.Available, feedBlock(1000, "start")))

	type read struct {
		revision uint64
		height   uint64
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= heads; i++ {
			publishHead(sup, "up1", feedBlock(uint64(1000+i), fmt.Sprintf("h%d", i)))
		}
	}()

	reads := make([]read, 0)
	takeReads := func() {
		snapshot := sup.GetUpstreamViews()
		if view, ok := snapshot.Views["up1"]; ok {
			reads = append(reads, read{snapshot.Revision, view.State.HeadData.Height})
		}
		if view, revision, ok := sup.GetUpstreamView("up1"); ok {
			reads = append(reads, read{revision, view.State.HeadData.Height})
		}
	}
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			takeReads()
		}
	}

	heightAt := make(map[uint64]uint64)
	var last uint64
	for range heads + 1 {
		event := readUpstreamStateEvent(t, sub)
		for _, change := range event.Changes {
			if head, ok := change.(*upstreams.HeadWrapper); ok {
				heightAt[event.Revision] = head.Head.Height
			}
		}
		last = event.Revision
	}
	require.Eventually(t, func() bool { return sup.GetUpstreamViews().Revision == last }, time.Second, 5*time.Millisecond)
	takeReads()

	require.NotEmpty(t, reads)
	for _, taken := range reads {
		assert.Equal(t, heightAt[taken.revision], taken.height, "read at revision %d", taken.revision)
	}
}

func TestUpstreamFeedSingleReadReturnsViewAndRevision(t *testing.T) {
	sup := newFeedSupervisor(t.Context())
	sub := sup.SubscribeUpstreamStates("test")
	state := indexedState("00001", protocol.Available, feedBlock(10, "a"))
	publishState(sup, "up1", state)
	event := readUpstreamStateEvent(t, sub)

	view, revision, known := sup.GetUpstreamView("up1")

	require.True(t, known)
	assert.Same(t, state, view.State)
	assert.NotNil(t, view.SubMethods)
	assert.Equal(t, event.Revision, revision)
	assert.Equal(t, everyUpstreamKind, wrapperKinds(view.Wrappers("up1")))
}

// a state built by hand in a test or by a new chain specific may leave parts unset
func TestUpstreamViewWrappersTolerateSparseState(t *testing.T) {
	sparse := upstreams.UpstreamView{State: &protocol.UpstreamState{}}
	assert.Equal(t, everyUpstreamKind, wrapperKinds(sparse.Wrappers("up1")))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := newFeedSupervisor(ctx)
	sub := sup.SubscribeUpstreamStates("test")

	publishState(sup, "up1", &protocol.UpstreamState{Status: protocol.Unavailable})
	publishState(sup, "up1", &protocol.UpstreamState{Status: protocol.Unavailable, Caps: mapset.NewThreadUnsafeSet(protocol.WsCap)})

	assert.Equal(t, everyUpstreamKind, wrapperKinds(readUpstreamStateEvent(t, sub).Changes))
	assertNoUpstreamStateEvent(t, sub)
}
