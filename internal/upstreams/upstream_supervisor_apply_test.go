package upstreams

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	applyWait = 2 * time.Second
	applyTick = 10 * time.Millisecond
)

// applyFixture runs a supervisor whose upstreams are plain GenericUpstreams without
// processors or connectors: Start publishes an Available state and nothing else, which is
// all the chain supervisor needs to register them.
type applyFixture struct {
	t          *testing.T
	supervisor *GenericUpstreamSupervisor
	cancel     context.CancelFunc

	mu       sync.Mutex
	created  map[string]int                                       // creation attempts per id
	emitters map[string]func(protocol.AbstractUpstreamStateEvent) // the latest instance's emitter
	failing  mapset.Set[string]                                   // ids whose creation fails
}

func newApplyFixture(t *testing.T) *applyFixture {
	t.Helper()
	f := newApplyFixtureWithoutEventProcessing(t)
	go f.supervisor.processEvents()
	return f
}

// newApplyFixtureWithoutEventProcessing leaves processEvents stopped, so a test can hold
// the supervisor's events back and observe what an apply waits for.
func newApplyFixtureWithoutEventProcessing(t *testing.T) *applyFixture {
	t.Helper()
	specs_utils.LoadMethodSpecs()

	ctx, cancel := context.WithCancel(context.Background())
	supervisor := NewGenericUpstreamSupervisor(ctx, &config.UpstreamConfig{FailsafeConfig: &config.FailsafeConfig{}}, nil, nil, nil, "").(*GenericUpstreamSupervisor)
	f := &applyFixture{
		t:          t,
		supervisor: supervisor,
		cancel:     cancel,
		created:    make(map[string]int),
		emitters:   make(map[string]func(protocol.AbstractUpstreamStateEvent)),
		failing:    mapset.NewSet[string](),
	}
	supervisor.removalGrace = 0
	supervisor.createUpstream = f.create
	t.Cleanup(cancel)
	return f
}

func (f *applyFixture) create(_ context.Context, conf *config.Upstream, _ failsafe.Executor[protocol.ResponseHolder], index int) (Upstream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created[conf.Id]++
	if f.failing.Contains(conf.Id) {
		return nil, errors.New("creation failed")
	}
	upstreamMethods, err := methods.NewUpstreamMethods("eth", conf.Methods, nil)
	require.NoError(f.t, err)
	indexHex := fmt.Sprintf("%05x", index)
	state := utils.NewAtomic[protocol.UpstreamState]()
	state.Store(protocol.DefaultUpstreamState(upstreamMethods, mapset.NewThreadUnsafeSet[protocol.Cap](), indexHex, nil, nil))
	stateChan := make(chan protocol.AbstractUpstreamStateEvent, 100)
	up := NewGenericUpstreamWithParams(conf.Id, chains.GetChain(conf.ChainName).Chain, nil, conf, indexHex, state, nil, &stateChan, nil, false)
	f.emitters[conf.Id] = func(event protocol.AbstractUpstreamStateEvent) { stateChan <- event }
	return up, nil
}

func (f *applyFixture) creations(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created[id]
}

func (f *applyFixture) emit(id string, event protocol.AbstractUpstreamStateEvent) {
	f.mu.Lock()
	emitter := f.emitters[id]
	f.mu.Unlock()
	emitter(event)
}

func (f *applyFixture) chainIds(chain chains.Chain) func() []string {
	return func() []string {
		cs := f.supervisor.GetChainSupervisor(chain)
		if cs == nil {
			return nil
		}
		return cs.GetUpstreamIds()
	}
}

func upstreamConf(id, chain string, pollInterval time.Duration) *config.Upstream {
	return &config.Upstream{
		Id:             id,
		ChainName:      chain,
		PollInterval:   pollInterval,
		Methods:        &config.MethodsConfig{BanDuration: time.Minute},
		FailsafeConfig: &config.FailsafeConfig{},
		Connectors:     []*config.ApiConnectorConfig{{Type: "json-rpc", Url: "http://localhost"}},
	}
}

func TestApplyUpstreamsAddsEverythingOnEmptySupervisor(t *testing.T) {
	f := newApplyFixture(t)

	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute), upstreamConf("b", "ethereum", time.Minute)})

	assert.Equal(t, UpstreamsDiff{Added: []string{"a", "b"}}, diff)
	assert.Eventually(t, func() bool { return assert.ObjectsAreEqual([]string{"a", "b"}, f.chainIds(chains.ETHEREUM)()) }, applyWait, applyTick)
	require.NotNil(t, f.supervisor.GetUpstream("a"))
	require.NotNil(t, f.supervisor.GetUpstream("b"))
}

func TestApplyUpstreamsRemovesMissingAndStopsIt(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute), upstreamConf("b", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 2 }, applyWait, applyTick)
	old := f.supervisor.GetUpstream("b")

	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})

	assert.Equal(t, UpstreamsDiff{Removed: []string{"b"}}, diff)
	assert.Eventually(t, func() bool { return assert.ObjectsAreEqual([]string{"a"}, f.chainIds(chains.ETHEREUM)()) }, applyWait, applyTick)
	assert.Nil(t, f.supervisor.GetUpstream("b"))
	assert.False(t, old.Running())
}

// A state event the old instance publishes after its deregistration - it is still running
// during the grace - must never reach the chain supervisor: the forwarder is gone, so the
// event has no subscriber and cannot resurrect the upstream.
func TestApplyUpstreamsIgnoresEventsOfRemovedUpstream(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.removalGrace = 300 * time.Millisecond
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)

	applied := make(chan struct{})
	go func() {
		f.supervisor.ApplyUpstreams(nil)
		close(applied)
	}()
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 0 }, applyWait, applyTick)

	// still inside the grace: the instance is alive and publishes a changed state
	f.emit("a", &protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	f.emit("a", &protocol.StatusUpstreamStateEvent{Status: protocol.Available})
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, f.chainIds(chains.ETHEREUM)())

	<-applied
	assert.Empty(t, f.chainIds(chains.ETHEREUM)())
}

func TestApplyUpstreamsReplacesChangedUpstream(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)
	old := f.supervisor.GetUpstream("a")

	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", 2*time.Minute)})

	assert.Equal(t, UpstreamsDiff{Changed: []string{"a"}}, diff)
	assert.False(t, old.Running())
	replacement := f.supervisor.GetUpstream("a")
	require.NotNil(t, replacement)
	assert.NotSame(t, old, replacement)
	assert.NotEqual(t, old.GetHashIndex(), replacement.GetHashIndex())
	// the chain supervisor ends up with the new instance's state, never the old one's
	assert.Eventually(t, func() bool {
		cs := f.supervisor.GetChainSupervisor(chains.ETHEREUM)
		state := cs.GetUpstreamState("a")
		return state != nil && state.UpstreamIndex == replacement.GetHashIndex()
	}, applyWait, applyTick)
}

func TestApplyUpstreamsLeavesUnchangedUpstreamAlone(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)
	before := f.supervisor.GetUpstream("a")

	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})

	assert.True(t, diff.IsEmpty())
	assert.Same(t, before, f.supervisor.GetUpstream("a"))
	assert.Equal(t, 1, f.creations("a"))
}

func TestApplyUpstreamsChangedChainMovesUpstream(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)

	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "polygon", time.Minute)})

	assert.Equal(t, UpstreamsDiff{Changed: []string{"a"}}, diff)
	assert.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 0 }, applyWait, applyTick)
	assert.Eventually(t, func() bool { return assert.ObjectsAreEqual([]string{"a"}, f.chainIds(chains.POLYGON)()) }, applyWait, applyTick)
}

func TestApplyUpstreamsRetriesFailedCreationOnNextApply(t *testing.T) {
	f := newApplyFixture(t)
	f.failing.Add("a")
	confs := []*config.Upstream{upstreamConf("a", "ethereum", time.Minute)}

	first := f.supervisor.ApplyUpstreams(confs)
	assert.Equal(t, UpstreamsDiff{Failed: []string{"a"}}, first)
	assert.Nil(t, f.supervisor.GetUpstream("a"))

	f.failing.Remove("a")
	second := f.supervisor.ApplyUpstreams(confs)

	assert.Equal(t, UpstreamsDiff{Added: []string{"a"}}, second)
	assert.Equal(t, 2, f.creations("a"))
	require.NotNil(t, f.supervisor.GetUpstream("a"))
}

// During the grace the removed upstream is already out of the chain supervisor but still
// resolvable and running, so requests already on it can finish.
func TestApplyUpstreamsKeepsRemovedUpstreamResolvableDuringGrace(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.removalGrace = 300 * time.Millisecond
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)
	old := f.supervisor.GetUpstream("a")

	applied := make(chan struct{})
	go func() {
		f.supervisor.ApplyUpstreams(nil)
		close(applied)
	}()

	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 0 }, applyWait, applyTick)
	assert.Same(t, old, f.supervisor.GetUpstream("a"))
	assert.True(t, old.Running())

	<-applied
	assert.Nil(t, f.supervisor.GetUpstream("a"))
	assert.False(t, old.Running())
}

func TestApplyUpstreamsRemovesInParallel(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.removalGrace = 300 * time.Millisecond
	f.supervisor.ApplyUpstreams([]*config.Upstream{
		upstreamConf("a", "ethereum", time.Minute), upstreamConf("b", "ethereum", time.Minute), upstreamConf("c", "ethereum", time.Minute),
	})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 3 }, applyWait, applyTick)

	started := time.Now()
	f.supervisor.ApplyUpstreams(nil)

	assert.Less(t, time.Since(started), 2*f.supervisor.removalGrace)
}

// A reload that lands while the initial apply is still running must wait for it instead
// of interleaving with it.
func TestApplyUpstreamsSerializesWithConcurrentApply(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.removalGrace = 200 * time.Millisecond
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)

	var wg sync.WaitGroup
	var first, second UpstreamsDiff
	wg.Go(func() { first = f.supervisor.ApplyUpstreams(nil) })
	time.Sleep(50 * time.Millisecond) // the first apply is inside its grace now
	wg.Go(func() {
		second = f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	})
	wg.Wait()

	assert.Equal(t, UpstreamsDiff{Removed: []string{"a"}}, first)
	assert.Equal(t, UpstreamsDiff{Added: []string{"a"}}, second)
	assert.Eventually(t, func() bool { return assert.ObjectsAreEqual([]string{"a"}, f.chainIds(chains.ETHEREUM)()) }, applyWait, applyTick)
}

// A removal is complete only once the supervisor's event loop has handled the Remove event:
// the loop resolves that event against the upstream map when it dequeues it, so a
// replacement stored under the same id before then would be the instance it partially stops.
func TestApplyUpstreamsRemovalWaitsForTheRemoveEventToBeHandled(t *testing.T) {
	f := newApplyFixtureWithoutEventProcessing(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	old := f.supervisor.GetUpstream("a")
	require.NotNil(t, old)

	applied := make(chan struct{})
	go func() {
		f.supervisor.ApplyUpstreams(nil)
		close(applied)
	}()

	select {
	case <-applied:
		t.Fatal("the removal completed before its Remove event was handled")
	case <-time.After(200 * time.Millisecond):
	}
	assert.True(t, old.Running())

	go f.supervisor.processEvents()

	select {
	case <-applied:
	case <-time.After(applyWait):
		t.Fatal("the removal did not complete once the Remove event was handled")
	}
	assert.False(t, old.Running())
	assert.Nil(t, f.supervisor.GetUpstream("a"))
	assert.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 0 }, applyWait, applyTick)
}

// A replacement is built before the old instance is touched: when it cannot be built, the
// old instance keeps running on its old settings and the id is reported as failed, not changed.
func TestApplyUpstreamsKeepsOldInstanceWhenReplacementFails(t *testing.T) {
	f := newApplyFixture(t)
	f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", time.Minute)})
	require.Eventually(t, func() bool { return len(f.chainIds(chains.ETHEREUM)()) == 1 }, applyWait, applyTick)
	old := f.supervisor.GetUpstream("a")

	f.failing.Add("a")
	diff := f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", 2*time.Minute)})

	assert.Equal(t, UpstreamsDiff{Failed: []string{"a"}}, diff)
	assert.Same(t, old, f.supervisor.GetUpstream("a"))
	assert.True(t, old.Running())
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, []string{"a"}, f.chainIds(chains.ETHEREUM)())

	// the old instance still runs on its old config, so the next apply tries the change again
	f.failing.Remove("a")
	diff = f.supervisor.ApplyUpstreams([]*config.Upstream{upstreamConf("a", "ethereum", 2*time.Minute)})
	assert.Equal(t, UpstreamsDiff{Changed: []string{"a"}}, diff)
	assert.False(t, old.Running())
	assert.NotSame(t, old, f.supervisor.GetUpstream("a"))
}

func TestApplyUpstreamsDiffIsNotEmptyWithFailures(t *testing.T) {
	assert.False(t, UpstreamsDiff{Failed: []string{"a"}}.IsEmpty())
	assert.Equal(t, "added [], removed [], changed [], failed [a]", UpstreamsDiff{Failed: []string{"a"}}.String())
}
