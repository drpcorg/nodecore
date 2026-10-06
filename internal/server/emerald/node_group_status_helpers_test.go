package emerald_test

import (
	"slices"
	"sync"
	"sync/atomic"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/mock"
)

type upstreamsChainSupervisor struct {
	*fakeChainSupervisor
	mu      sync.RWMutex
	states  map[string]*protocol.UpstreamState
	changed utils.Signal
	reads   atomic.Int64
}

func newUpstreamsChainSupervisor(chain chains.Chain) *upstreamsChainSupervisor {
	return &upstreamsChainSupervisor{
		fakeChainSupervisor: newFakeChainSupervisor(chain, newChainState(chain, protocol.NewBlockWithHeight(100), nil)),
		states:              make(map[string]*protocol.UpstreamState),
	}
}

func (s *upstreamsChainSupervisor) UpstreamsChanged() <-chan struct{} {
	return s.changed.C()
}

func (s *upstreamsChainSupervisor) GetUpstreamIds() []string {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.states))
	for id := range s.states {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (s *upstreamsChainSupervisor) GetUpstreamState(id string) *protocol.UpstreamState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[id]
}

func (s *upstreamsChainSupervisor) set(id string, state *protocol.UpstreamState) {
	s.mu.Lock()
	s.states[id] = state
	s.mu.Unlock()
	s.changed.Notify()
}

func (s *upstreamsChainSupervisor) remove(id string) {
	s.mu.Lock()
	delete(s.states, id)
	s.mu.Unlock()
	s.changed.Notify()
}

// update stores a copy of the upstream's snapshot, the way the supervisor does
func (s *upstreamsChainSupervisor) update(id string, change func(*protocol.UpstreamState)) {
	s.mu.Lock()
	next := *s.states[id]
	change(&next)
	s.states[id] = &next
	s.mu.Unlock()
	s.changed.Notify()
}

type chainsUpstreamSupervisor struct {
	*mocks.UpstreamSupervisorMock
	mu     sync.Mutex
	chains []upstreams.ChainSupervisor
	events *utils.SubscriptionManager[upstreams.ChainSupervisorEvent]
}

func newChainsUpstreamSupervisor(chainSupervisors ...upstreams.ChainSupervisor) *chainsUpstreamSupervisor {
	return &chainsUpstreamSupervisor{
		UpstreamSupervisorMock: mocks.NewUpstreamSupervisorMock(),
		chains:                 chainSupervisors,
		events:                 utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("test-chain-supervisors"),
	}
}

func (s *chainsUpstreamSupervisor) SubscribeChainSupervisor(name string) *utils.Subscription[upstreams.ChainSupervisorEvent] {
	return s.events.Subscribe(name)
}

func (s *chainsUpstreamSupervisor) GetChainSupervisors() []upstreams.ChainSupervisor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.chains)
}

// add registers a chain supervisor the way the upstream supervisor does:
// stored first, then announced
func (s *chainsUpstreamSupervisor) add(chainSupervisor upstreams.ChainSupervisor) {
	s.mu.Lock()
	s.chains = append(s.chains, chainSupervisor)
	s.mu.Unlock()
	s.events.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: chainSupervisor})
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
