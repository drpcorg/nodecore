package upstreams

import (
	"context"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/require"
)

func newUpstreamStateSupervisor(t *testing.T) *GenericChainSupervisor {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	supervisor := NewGenericChainSupervisor(t.Context(), chains.ETHEREUM, choice.NewHeightForkChoice, nil, false, nil)
	supervisor.Start()
	return supervisor
}

func upstreamState(height uint64) *protocol.UpstreamState {
	state := protocol.DefaultUpstreamState(methods.NewChainMethods(nil), mapset.NewThreadUnsafeSet[protocol.Cap](), "00001", nil, nil)
	state.HeadData = protocol.NewBlockWithHeight(height)
	return &state
}

func storeUpstreamState(t *testing.T, supervisor *GenericChainSupervisor, id string, state *protocol.UpstreamState) {
	t.Helper()
	supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.StateUpstreamEvent{State: state}})
	require.Eventually(t, func() bool { return supervisor.GetUpstreamState(id) == state }, time.Second, time.Millisecond)
}

func nextUpstreamStateEvent(t *testing.T, events <-chan *UpstreamStateEvent) *UpstreamStateEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("upstream state event timed out")
		return nil
	}
}

func upstreamEventHeight(event *UpstreamStateEvent) uint64 {
	if event.Full {
		return event.State.HeadData.Head.Height
	}
	if len(event.Wrappers) == 1 {
		if head, ok := event.Wrappers[0].(*HeadWrapper); ok {
			return head.Head.Height
		}
	}
	return 0
}

func runUpstreamStateSubscription(t *testing.T, supervisor *GenericChainSupervisor, interval, resync time.Duration, output chan<- *UpstreamStateEvent) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		supervisor.forwardUpstreamStates(ctx, "test_upstream_states", interval, resync, output)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("subscription did not stop after cancellation")
		}
	})
	return cancel, done
}

func TestUpstreamStatesCoalescesPerSubscriberAndSeparatesMetadataAndHead(t *testing.T) {
	supervisor := newUpstreamStateSupervisor(t)
	initial := upstreamState(100)
	storeUpstreamState(t, supervisor, "a", initial)
	immediate, delayed := make(chan *UpstreamStateEvent, 20), make(chan *UpstreamStateEvent, 20)
	runUpstreamStateSubscription(t, supervisor, 0, time.Hour, immediate)
	const interval = 200 * time.Millisecond
	runUpstreamStateSubscription(t, supervisor, interval, time.Hour, delayed)
	require.True(t, nextUpstreamStateEvent(t, immediate).Full)
	require.True(t, nextUpstreamStateEvent(t, delayed).Full)
	firstAt := time.Now()

	for height := uint64(101); height <= 103; height++ {
		supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "a", EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: protocol.NewBlockWithHeight(height)}})
		require.Equal(t, height, upstreamEventHeight(nextUpstreamStateEvent(t, immediate)))
	}
	changed := *supervisor.GetUpstreamState("a")
	changed.UpstreamIndex = "00002"
	callMethods, err := methods.NewUpstreamMethods(chains.GetMethodSpecNameByChain(chains.ETHEREUM), nil, nil)
	require.NoError(t, err)
	changed.UpstreamMethods = callMethods
	changed.HeadData = protocol.NewBlockWithHeight(104)
	changed.Labels = protocol.NewLabels()
	changed.Labels.AddLabel("client_type", "reth")
	changed.Status = protocol.Syncing
	changed.BlockInfo = protocol.NewBlockInfo()
	changed.BlockInfo.AddBlock(protocol.NewBlockWithHeight(90), protocol.FinalizedBlock)
	changed.LowerBoundsInfo = protocol.NewLowerBoundInfo()
	changed.LowerBoundsInfo.AddLowerBound(protocol.LowerBoundData{Type: protocol.StateBound, Bound: 70})
	storeUpstreamState(t, supervisor, "a", &changed)
	for _, output := range []chan *UpstreamStateEvent{immediate, delayed} {
		metadata := nextUpstreamStateEvent(t, output)
		if output == immediate {
			require.Empty(t, delayed)
		} else {
			require.GreaterOrEqual(t, time.Since(firstAt), interval-10*time.Millisecond)
		}
		require.False(t, metadata.Full)
		require.Equal(t, "00002", metadata.Index)
		require.Zero(t, upstreamEventHeight(metadata))
		require.Len(t, metadata.Wrappers, 6)
		require.Equal(t, protocol.Syncing, metadata.Wrappers[0].(*StatusWrapper).Status)
		require.Contains(t, metadata.Wrappers[1].(*MethodsWrapper).Methods, "eth_call")
		require.Equal(t, int64(70), metadata.Wrappers[2].(*LowerBoundsWrapper).LowerBounds[0].Bound)
		require.Equal(t, uint64(90), metadata.Wrappers[3].(*BlocksWrapper).Blocks[protocol.FinalizedBlock].Height)
		require.Equal(t, "reth", metadata.Wrappers[5].(*LabelsWrapper).Labels[0].Labels["client_type"])
		head := nextUpstreamStateEvent(t, output)
		require.False(t, head.Full)
		require.Equal(t, "00002", head.Index)
		require.Equal(t, uint64(104), upstreamEventHeight(head))
		require.Empty(t, output)
	}
}

func TestUpstreamStatesStartupDuringUpdatesNeverRegresses(t *testing.T) {
	supervisor := newUpstreamStateSupervisor(t)
	storeUpstreamState(t, supervisor, "a", upstreamState(1))
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 12 {
		workers.Go(func() {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			output := make(chan *UpstreamStateEvent, 8)
			<-start
			supervisor.SubscribeUpstreamStates(ctx, "startup_test", 0, 20*time.Millisecond, output)
			var last uint64
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			for last < 200 {
				select {
				case event := <-output:
					height := upstreamEventHeight(event)
					if height == 0 {
						continue
					}
					if height < last {
						t.Errorf("head regressed from %d to %d", last, height)
						return
					}
					last = height
				case <-deadline.C:
					t.Error("subscriber missed latest head")
					return
				}
			}
		})
	}
	close(start)
	for height := uint64(2); height <= 200; height++ {
		supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "a", EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: protocol.NewBlockWithHeight(height)}})
	}
	workers.Wait()
}

func TestUpstreamStatesSlowSubscriberResyncRepairsDroppedRemovalAndRecovers(t *testing.T) {
	supervisor := newUpstreamStateSupervisor(t)
	storeUpstreamState(t, supervisor, "a", upstreamState(1))
	recovered := upstreamState(10)
	storeUpstreamState(t, supervisor, "b", recovered)
	output := make(chan *UpstreamStateEvent)
	runUpstreamStateSubscription(t, supervisor, 0, 20*time.Millisecond, output)
	require.True(t, nextUpstreamStateEvent(t, output).Full)
	require.True(t, nextUpstreamStateEvent(t, output).Full)

	// No output is read while more than the bounded input queue can hold arrives.
	for height := uint64(2); height <= 250; height++ {
		supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "a", EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: protocol.NewBlockWithHeight(height)}})
	}
	supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "b", EventType: &protocol.RemoveUpstreamEvent{}})
	require.Eventually(t, func() bool { return supervisor.GetUpstreamState("b") == nil }, time.Second, time.Millisecond)
	// Routing and authoritative snapshots remain responsive despite blocked output.
	snapshot, ok := supervisor.requestUpstreamStates(t.Context(), "", false)
	require.True(t, ok)
	require.Equal(t, uint64(250), snapshot.states["a"].HeadData.Height)

	removals, latest := 0, false
	for removals < 2 || !latest {
		event := nextUpstreamStateEvent(t, output)
		if event.ID == "a" && upstreamEventHeight(event) == 250 {
			latest = true
		}
		if event.ID == "b" && len(event.Wrappers) > 0 {
			if status, ok := event.Wrappers[0].(*StatusWrapper); ok && status.Status == protocol.Unavailable {
				removals++
			}
		}
	}
	recovered.UpstreamIndex = "00002"
	supervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "b", EventType: &protocol.ValidUpstreamEvent{State: recovered}})
	for {
		event := nextUpstreamStateEvent(t, output)
		if event.ID != "b" || event.Index != "00002" || len(event.Wrappers) < 2 {
			continue
		}
		require.Equal(t, "00002", event.Index)
		require.Equal(t, protocol.Available, event.Wrappers[0].(*StatusWrapper).Status)
		require.Zero(t, upstreamEventHeight(event))
		require.Equal(t, uint64(10), upstreamEventHeight(nextUpstreamStateEvent(t, output)))
		break
	}
}

func TestUpstreamStatesCancellationReleasesBlockedOutputAndRegistration(t *testing.T) {
	supervisor := newUpstreamStateSupervisor(t)
	storeUpstreamState(t, supervisor, "a", upstreamState(1))
	cancel, done := runUpstreamStateSubscription(t, supervisor, time.Hour, time.Hour, make(chan *UpstreamStateEvent))
	// Ensure the registration command has run and the worker can block on output.
	require.Eventually(t, func() bool {
		snapshot, _ := supervisor.requestUpstreamStates(t.Context(), "", false)
		return len(snapshot.states) == 1
	}, time.Second, time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked forwarding ignored cancellation")
	}
	// A supervisor that never started cannot consume registration commands.
	ctx, stop := context.WithCancel(t.Context())
	stopped := NewGenericChainSupervisor(ctx, chains.ETHEREUM, choice.NewHeightForkChoice, nil, false, nil)
	_, done = runUpstreamStateSubscription(t, stopped, 0, time.Hour, make(chan *UpstreamStateEvent))
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("registration survived supervisor shutdown")
	}
}

func TestUpstreamStatesResyncMetadataDoesNotRefreshUnchangedHead(t *testing.T) {
	supervisor := newUpstreamStateSupervisor(t)
	storeUpstreamState(t, supervisor, "a", upstreamState(100))
	output := make(chan *UpstreamStateEvent, 10)
	runUpstreamStateSubscription(t, supervisor, 0, 15*time.Millisecond, output)
	require.Equal(t, uint64(100), upstreamEventHeight(nextUpstreamStateEvent(t, output)))
	for range 3 {
		event := nextUpstreamStateEvent(t, output)
		require.False(t, event.Full)
		require.Zero(t, upstreamEventHeight(event))
		require.Len(t, event.Wrappers, 6)
	}
}
