package subengine

import (
	"context"
	"testing"
	"time"

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
// drives directly.
type fakeChainSupervisor struct {
	sm    *utils.SubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]
	state upstreams.ChainSupervisorState
}

func newFakeChainSupervisor() *fakeChainSupervisor {
	return &fakeChainSupervisor{sm: utils.NewSubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]("fake")}
}

func (s *fakeChainSupervisor) publishHead(block protocol.Block, upstreamId string) {
	s.sm.Publish(&upstreams.ChainSupervisorStateWrapperEvent{
		Wrappers: []upstreams.ChainSupervisorStateWrapper{upstreams.NewHeadWrapper(block, upstreamId)},
	})
}

func (s *fakeChainSupervisor) Start()                                          {}
func (s *fakeChainSupervisor) GetChain() chains.Chain                          { return chains.ETHEREUM }
func (s *fakeChainSupervisor) GetChainState() upstreams.ChainSupervisorState   { return s.state }
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

// The head source forwards each head's RawData, stamps the producing upstream,
// and skips heads without RawData (polled blocks).
func TestNewHeadsSourceForwardsRawDataAndSkipsEmpty(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM)(ctx)
	require.NoError(t, err)
	defer src.Stop()

	chainSup.publishHead(protocol.Block{Height: 1, RawData: []byte(`{"number":"0x1"}`)}, "up1")
	first := <-src.Events
	assert.Equal(t, []byte(`{"number":"0x1"}`), first.Message)
	assert.Equal(t, "up1", first.UpstreamId)

	// a polled head (no RawData) is skipped; the next head with RawData arrives
	chainSup.publishHead(protocol.Block{Height: 2}, "up2")
	chainSup.publishHead(protocol.Block{Height: 3, RawData: []byte(`{"number":"0x3"}`)}, "up1")
	second := <-src.Events
	assert.Equal(t, []byte(`{"number":"0x3"}`), second.Message)
}

// The first subscriber is seeded with the current head if it carries RawData.
func TestNewHeadsSourceSeedsCurrentHead(t *testing.T) {
	chainSup := newFakeChainSupervisor()
	chainSup.state = upstreams.ChainSupervisorState{
		HeadData: upstreams.NewChainHeadData(protocol.Block{Height: 10, RawData: []byte(`{"number":"0xa"}`)}, "up1"),
	}
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := NewHeadsSourceBuilder(sup, chains.ETHEREUM)(ctx)
	require.NoError(t, err)
	defer src.Stop()

	select {
	case seeded := <-src.Events:
		assert.Equal(t, []byte(`{"number":"0xa"}`), seeded.Message)
		assert.Equal(t, "up1", seeded.UpstreamId)
	case <-time.After(time.Second):
		t.Fatal("expected the current head to be seeded")
	}
}
