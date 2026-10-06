package subengine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChainSupervisor is a minimal ChainSupervisor whose head feed the test
// drives directly. It records the filter it was given and whether the feed was
// unsubscribed.
type fakeChainSupervisor struct {
	feed chan upstreams.HeadFeedEvent

	mu           sync.Mutex
	filter       upstreams.FilterUpstream
	unsubscribed bool
}

func newFakeChainSupervisor() *fakeChainSupervisor {
	return &fakeChainSupervisor{feed: make(chan upstreams.HeadFeedEvent, 16)}
}

func (s *fakeChainSupervisor) SubscribeHead(_ string, filter upstreams.FilterUpstream) *upstreams.HeadFeedSubscription {
	s.mu.Lock()
	s.filter = filter
	s.mu.Unlock()
	return upstreams.NewHeadFeedSubscription(s.feed, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.unsubscribed {
			s.unsubscribed = true
			close(s.feed)
		}
	})
}

func (s *fakeChainSupervisor) wasUnsubscribed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsubscribed
}

func (s *fakeChainSupervisor) Start()                 {}
func (s *fakeChainSupervisor) GetChain() chains.Chain { return chains.ETHEREUM }
func (s *fakeChainSupervisor) GetChainState() upstreams.ChainSupervisorState {
	return upstreams.ChainSupervisorState{}
}
func (s *fakeChainSupervisor) GetMethod(string) *specs.Method { return nil }
func (s *fakeChainSupervisor) GetMethods() []string           { return nil }
func (s *fakeChainSupervisor) GetUpstreamState(string) *protocol.UpstreamState {
	return nil
}
func (s *fakeChainSupervisor) GetSortedUpstreamIds(upstreams.FilterUpstream, upstreams.SortUpstream) []string {
	return nil
}
func (s *fakeChainSupervisor) GetUpstreamIds() []string                    { return nil }
func (s *fakeChainSupervisor) NextIndex() uint64                           { return 0 }
func (s *fakeChainSupervisor) PublishUpstreamEvent(protocol.UpstreamEvent) {}
func (s *fakeChainSupervisor) SubscribeState(string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return nil
}

func (s *fakeChainSupervisor) UpstreamsChanged() <-chan struct{} {
	return nil
}

var _ upstreams.ChainSupervisor = (*fakeChainSupervisor)(nil)

func matchAll(string, *protocol.UpstreamState) bool { return true }

func buildNewHeads(t *testing.T, chainSup *fakeChainSupervisor, filter upstreams.FilterUpstream) (*Source, context.CancelFunc) {
	t.Helper()
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)
	ctx, cancel := context.WithCancel(context.Background())
	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM, filter)(ctx)
	require.NoError(t, err)
	return src, cancel
}

func readSource(t *testing.T, src *Source) protocol.SubResponse {
	t.Helper()
	select {
	case r, ok := <-src.Events:
		require.True(t, ok, "source closed unexpectedly")
		return r
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a source event")
		return nil
	}
}

// A head carrying RawData (the ws newHeads header) is forwarded verbatim and
// stamped with the producing upstream; a head without RawData (a polled block)
// is not a subscription notification and is skipped.
func TestNewHeadsSourceForwardsSubscriptionBlocks(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	src, cancel := buildNewHeads(t, chainSup, matchAll)
	defer cancel()

	header := []byte(`{"number":"0x1","hash":"0xaa"}`)
	chainSup.feed <- upstreams.HeadUpdated{Head: protocol.Block{Height: 1, RawData: header}, UpstreamId: "up1"}
	first := readSource(t, src)
	assert.Equal(t, "up1", first.GetUpstreamId())
	assert.Equal(t, header, first.GetMessage())

	chainSup.feed <- upstreams.HeadUpdated{Head: protocol.Block{Height: 2}, UpstreamId: "up2"} // polled: skipped
	next := []byte(`{"number":"0x3"}`)
	chainSup.feed <- upstreams.HeadUpdated{Head: protocol.Block{Height: 3, RawData: next}, UpstreamId: "up1"}
	assert.Equal(t, next, readSource(t, src).GetMessage())
}

// The feed going empty (the last matching upstream left) is a terminal frame so
// clients can fail over.
func TestNewHeadsSourceTerminatesOnEmptyFeed(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	src, cancel := buildNewHeads(t, chainSup, matchAll)
	defer cancel()

	chainSup.feed <- upstreams.HeadUpdated{Head: protocol.Block{Height: 1, RawData: []byte(`{"number":"0x1"}`)}, UpstreamId: "up1"}
	readSource(t, src)

	chainSup.feed <- upstreams.HeadFeedEmpty{}
	terminal := readSource(t, src)
	require.NotNil(t, terminal.GetError())
	_, open := <-src.Events
	assert.False(t, open, "source must close after the terminal frame")
	assert.True(t, chainSup.wasUnsubscribed())
}

// A feed that is empty at subscribe time seeds HeadFeedEmpty; the source
// terminates immediately instead of stalling.
func TestNewHeadsSourceTerminatesWhenEmptyAtStart(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	chainSup.feed <- upstreams.HeadFeedEmpty{} // the seed
	src, cancel := buildNewHeads(t, chainSup, matchAll)
	defer cancel()

	require.NotNil(t, readSource(t, src).GetError())
}

// Cancelling the source context unsubscribes the feed and closes the source.
func TestNewHeadsSourceUnsubscribesOnCancel(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	src, cancel := buildNewHeads(t, chainSup, matchAll)

	cancel()
	select {
	case _, open := <-src.Events:
		assert.False(t, open)
	case <-time.After(time.Second):
		t.Fatal("source not closed after cancel")
	}
	assert.Eventually(t, chainSup.wasUnsubscribed, time.Second, 10*time.Millisecond)
}

// The source subscribes its feed with the filter it was built with.
func TestNewHeadsSourcePassesFilterToFeed(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	onlyUp1 := func(id string, _ *protocol.UpstreamState) bool { return id == "up1" }
	_, cancel := buildNewHeads(t, chainSup, onlyUp1)
	defer cancel()

	chainSup.mu.Lock()
	filter := chainSup.filter
	chainSup.mu.Unlock()
	require.NotNil(t, filter)
	assert.True(t, filter("up1", &protocol.UpstreamState{}))
	assert.False(t, filter("up2", &protocol.UpstreamState{}))
}
