package subengine

import (
	"context"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChainSupervisor is a minimal ChainSupervisor whose head stream the test
// drives directly. Like the real supervisor it stores state before publishing,
// so GetChainState is authoritative when an event is observed.
type fakeChainSupervisor struct {
	sm    *utils.SubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]
	mu    sync.Mutex
	state upstreams.ChainSupervisorState
}

func newFakeChainSupervisor() *fakeChainSupervisor {
	return &fakeChainSupervisor{sm: utils.NewSubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]("fake")}
}

func (s *fakeChainSupervisor) setCaps(caps mapset.Set[protocol.Cap]) {
	s.mu.Lock()
	s.state.Caps = caps
	s.mu.Unlock()
}

func (s *fakeChainSupervisor) publishHead(block protocol.Block, upstreamId string) {
	s.sm.Publish(&upstreams.ChainSupervisorStateWrapperEvent{
		Wrappers: []upstreams.ChainSupervisorStateWrapper{upstreams.NewHeadWrapper(block, upstreamId)},
	})
}

func (s *fakeChainSupervisor) publishCaps(caps mapset.Set[protocol.Cap]) {
	s.setCaps(caps) // store before publishing, mirroring the real supervisor
	s.sm.Publish(&upstreams.ChainSupervisorStateWrapperEvent{
		Wrappers: []upstreams.ChainSupervisorStateWrapper{upstreams.NewCapsWrapper(caps)},
	})
}

func (s *fakeChainSupervisor) Start()                 {}
func (s *fakeChainSupervisor) GetChain() chains.Chain { return chains.ETHEREUM }
func (s *fakeChainSupervisor) GetChainState() upstreams.ChainSupervisorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}
func (s *fakeChainSupervisor) GetMethod(string) *specs.Method                  { return nil }
func (s *fakeChainSupervisor) GetMethods() []string                            { return nil }
func (s *fakeChainSupervisor) GetUpstreamState(string) *protocol.UpstreamState { return nil }
func (s *fakeChainSupervisor) GetSortedUpstreamIds(upstreams.FilterUpstream, upstreams.SortUpstream) []string {
	return nil
}
func (s *fakeChainSupervisor) GetUpstreamIds() []string                    { return nil }
func (s *fakeChainSupervisor) PublishUpstreamEvent(protocol.UpstreamEvent) {}
func (s *fakeChainSupervisor) SubscribeState(name string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return s.sm.Subscribe(name)
}

var _ upstreams.ChainSupervisor = (*fakeChainSupervisor)(nil)

// A subscription block (RawData = the ws newHeads header) is forwarded verbatim
// and stamped with the producing upstream; a head without RawData (a polled
// block) is not a subscription notification and is skipped.
func TestNewHeadsSourceForwardsSubscriptionBlocks(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	chainSup.setCaps(mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap))
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM)(ctx)
	require.NoError(t, err)
	defer src.Stop()

	header := []byte(`{"number":"0x1","hash":"0xaa"}`)
	chainSup.publishHead(protocol.Block{Height: 1, RawData: header}, "up1")
	first := <-src.Events
	assert.Equal(t, "up1", first.UpstreamId)
	assert.Equal(t, header, first.Message) // forwarded verbatim

	// a polled head (no RawData) is skipped; the next subscription block arrives
	chainSup.publishHead(protocol.Block{Height: 2}, "up2")
	next := []byte(`{"number":"0x3"}`)
	chainSup.publishHead(protocol.Block{Height: 3, RawData: next}, "up1")
	second := <-src.Events
	assert.Equal(t, next, second.Message)
}

// When NewHeadsCap disappears from the chain (the last ws-head upstream left),
// the source emits a terminal frame so clients can fail over.
func TestNewHeadsSourceTerminatesOnCapLoss(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	chainSup.setCaps(mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap))
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM)(ctx)
	require.NoError(t, err)
	defer src.Stop()

	// Drive a head first so the source is provably past its startup snapshot and
	// in the event loop, then drop NewHeadsCap: the loss is observed via the
	// state event and degrades the source.
	chainSup.publishHead(protocol.Block{Height: 1, RawData: []byte(`{"number":"0x1"}`)}, "up1")
	<-src.Events

	chainSup.publishCaps(mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))

	select {
	case terminal := <-src.Events:
		require.NotNil(t, terminal.Error)
	case <-time.After(time.Second):
		t.Fatal("expected a terminal frame on NewHeadsCap loss")
	}
}

// Caps changes arrive only as deltas; if NewHeadsCap is already gone when the
// source starts (no CapsWrapper will ever come), it degrades from the initial
// state snapshot instead of stalling silently.
func TestNewHeadsSourceTerminatesWhenCapAbsentAtStart(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	chainSup.setCaps(mapset.NewThreadUnsafeSet(protocol.WsCap)) // no NewHeadsCap
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM)(ctx)
	require.NoError(t, err)
	defer src.Stop()

	select {
	case terminal := <-src.Events:
		require.NotNil(t, terminal.Error)
	case <-time.After(time.Second):
		t.Fatal("expected an immediate terminal frame when NewHeadsCap is absent at start")
	}
}
