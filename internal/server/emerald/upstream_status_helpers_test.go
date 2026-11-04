package emerald_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type upstreamsChainSupervisor struct {
	*upstreams.GenericChainSupervisor
	t *testing.T
}

func newUpstreamsChainSupervisor(t *testing.T, chain chains.Chain) *upstreamsChainSupervisor {
	t.Helper()
	loadMethodSpecs(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	supervisor := upstreams.NewGenericChainSupervisor(ctx, chain, func() choice.ForkChoice { return choice.NewHeightForkChoice() }, nil, false, nil)
	supervisor.Start()
	return &upstreamsChainSupervisor{GenericChainSupervisor: supervisor, t: t}
}

func (s *upstreamsChainSupervisor) set(id string, state *protocol.UpstreamState) {
	s.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.ValidUpstreamEvent{State: state}})
	s.waitState(id, state)
}

func (s *upstreamsChainSupervisor) remove(id string) {
	s.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.RemoveUpstreamEvent{}})
	require.Eventually(s.t, func() bool { return s.GetUpstreamState(id) == nil }, time.Second, time.Millisecond)
}

func (s *upstreamsChainSupervisor) update(id string, change func(*protocol.UpstreamState)) {
	next := *s.GetUpstreamState(id)
	change(&next)
	s.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.StateUpstreamEvent{State: &next}})
	s.waitState(id, &next)
}

func (s *upstreamsChainSupervisor) waitState(id string, state *protocol.UpstreamState) {
	require.Eventually(s.t, func() bool { return s.GetUpstreamState(id) == state }, time.Second, time.Millisecond)
}

type chainsUpstreamSupervisor struct {
	*mocks.UpstreamSupervisorMock
	chains *utils.CMap[chains.Chain, upstreams.ChainSupervisor]
	events *utils.SubscriptionManager[upstreams.ChainSupervisorEvent]
}

func newChainsUpstreamSupervisor(chainSupervisors ...upstreams.ChainSupervisor) *chainsUpstreamSupervisor {
	supervisor := &chainsUpstreamSupervisor{
		UpstreamSupervisorMock: mocks.NewUpstreamSupervisorMock(),
		chains:                 utils.NewCMap[chains.Chain, upstreams.ChainSupervisor](),
		events:                 utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("test-chain-supervisors"),
	}
	for _, chain := range chainSupervisors {
		supervisor.chains.Store(chain.GetChain(), chain)
	}
	return supervisor
}

func (s *chainsUpstreamSupervisor) SubscribeChainSupervisor(name string) *utils.Subscription[upstreams.ChainSupervisorEvent] {
	return s.events.Subscribe(name)
}

func (s *chainsUpstreamSupervisor) GetChainSupervisors() []upstreams.ChainSupervisor {
	var result []upstreams.ChainSupervisor
	s.chains.Range(func(_ chains.Chain, chain upstreams.ChainSupervisor) bool {
		result = append(result, chain)
		return true
	})
	return result
}

func testUpstreamState(height uint64, methods ...string) *protocol.UpstreamState {
	methodsMock := newMethodsMockWithSupported(methods...)
	methodsMock.On("GetMethod", mock.Anything).Return(nil)
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	state.HeadData = protocol.NewBlockWithHeight(height)
	return &state
}

func chainRef(chain chains.Chain) dshackle.ChainRef {
	return dshackle.ChainRef(chains.GetChain(chain.String()).GrpcId)
}
