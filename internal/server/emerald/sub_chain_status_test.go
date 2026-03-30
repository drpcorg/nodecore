package emerald_test

import (
	"context"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type subscribeChainStatusStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	responses []*dshackle.SubscribeChainStatusResponse
	sent      chan struct{}
}

type fakeChainSupervisor struct {
	chain      chains.Chain
	mu         sync.RWMutex
	state      upstreams.ChainSupervisorState
	subManager *utils.SubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]
}

func newSubscribeChainStatusStream() *subscribeChainStatusStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &subscribeChainStatusStream{
		ctx:    ctx,
		cancel: cancel,
		sent:   make(chan struct{}, 100),
	}
}

func (s *subscribeChainStatusStream) SetHeader(metadata.MD) error  { return nil }
func (s *subscribeChainStatusStream) SendHeader(metadata.MD) error { return nil }
func (s *subscribeChainStatusStream) SetTrailer(metadata.MD)       {}
func (s *subscribeChainStatusStream) Context() context.Context     { return s.ctx }
func (s *subscribeChainStatusStream) SendMsg(any) error            { return nil }
func (s *subscribeChainStatusStream) RecvMsg(any) error            { return nil }

func (s *subscribeChainStatusStream) Send(resp *dshackle.SubscribeChainStatusResponse) error {
	s.mu.Lock()
	s.responses = append(s.responses, proto.Clone(resp).(*dshackle.SubscribeChainStatusResponse))
	s.mu.Unlock()

	select {
	case s.sent <- struct{}{}:
	default:
	}
	return nil
}

func (s *subscribeChainStatusStream) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.responses)
}

func (s *subscribeChainStatusStream) ResponseAt(index int) *dshackle.SubscribeChainStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return proto.Clone(s.responses[index]).(*dshackle.SubscribeChainStatusResponse)
}

func (s *subscribeChainStatusStream) Sent() <-chan struct{} {
	return s.sent
}

func newFakeChainSupervisor(chain chains.Chain, state upstreams.ChainSupervisorState) *fakeChainSupervisor {
	return &fakeChainSupervisor{
		chain:      chain,
		state:      state,
		subManager: utils.NewSubscriptionManager[*upstreams.ChainSupervisorStateWrapperEvent]("fake-chain-supervisor"),
	}
}

func (s *fakeChainSupervisor) Start() {}

func (s *fakeChainSupervisor) GetChain() chains.Chain {
	return s.chain
}

func (s *fakeChainSupervisor) GetChainState() upstreams.ChainSupervisorState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.state
}

func (s *fakeChainSupervisor) GetMethod(string) *specs.Method {
	return nil
}

func (s *fakeChainSupervisor) GetMethods() []string {
	return nil
}

func (s *fakeChainSupervisor) GetUpstreamState(string) *protocol.UpstreamState {
	return nil
}

func (s *fakeChainSupervisor) GetSortedUpstreamIds(upstreams.FilterUpstream, upstreams.SortUpstream) []string {
	return nil
}

func (s *fakeChainSupervisor) GetUpstreamIds() []string {
	return nil
}

func (s *fakeChainSupervisor) PublishUpstreamEvent(protocol.UpstreamEvent) {}

func (s *fakeChainSupervisor) SubscribeState(name string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return s.subManager.Subscribe(name)
}

func (s *fakeChainSupervisor) SetState(state upstreams.ChainSupervisorState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = state
}

func (s *fakeChainSupervisor) PublishStateEvent(wrappers ...upstreams.ChainSupervisorStateWrapper) {
	s.subManager.Publish(&upstreams.ChainSupervisorStateWrapperEvent{Wrappers: wrappers})
}

func loadMethodSpecs(t *testing.T) {
	t.Helper()

	require.NoError(t, specs.NewMethodSpecLoader().Load())
}

func newMethodsMockWithSupported(methods ...string) *mocks.MethodsMock {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string](methods...))
	return methodsMock
}

func newChainState(chain chains.Chain, head protocol.Block, methods []string, caps ...protocol.Cap) upstreams.ChainSupervisorState {
	methodsMock := newMethodsMockWithSupported(methods...)
	state := upstreams.ChainSupervisorState{
		Status:      protocol.Available,
		Methods:     methodsMock,
		Blocks:      map[protocol.BlockType]protocol.Block{},
		LowerBounds: map[protocol.LowerBoundType]protocol.LowerBoundData{},
		SubMethods:  mapset.NewThreadUnsafeSet[string](),
	}
	if !head.IsEmptyByHeight() {
		state.HeadData = upstreams.NewChainHeadData(head, "up-1")
	}
	if len(caps) > 0 {
		state.SubMethods = specs.GetSubMethods(chains.GetMethodSpecNameByChain(chain))
	}
	return state
}

func TestSubscribeChainStatus_NilSupervisorReturnsError(t *testing.T) {
	err := emerald.SubscribeChainStatus(nil, newSubscribeChainStatusStream())

	require.Error(t, err)
	assert.Equal(t, "upstream supervisor cannot be nil", err.Error())
}

func TestSubscribeChainStatus_SendsFullResponseForExistingSupervisor(t *testing.T) {
	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)

	response := stream.ResponseAt(0)
	require.NotNil(t, response.ChainDescription)
	assert.True(t, response.FullResponse)
	assert.NotNil(t, response.BuildInfo)
	assert.Equal(t, uint32(chains.GetChain(chains.ARBITRUM.String()).GrpcId), uint32(response.ChainDescription.Chain))
	assert.Len(t, response.ChainDescription.ChainEvent, 7)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_WaitsForHeadBeforeFirstResponse(t *testing.T) {
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	select {
	case <-stream.Sent():
		t.Fatal("received response before head event")
	case <-time.After(100 * time.Millisecond):
	}

	head := protocol.NewBlockWithHeight(100)
	chainSupervisor.SetState(newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	chainSupervisor.PublishStateEvent(upstreams.NewHeadWrapper(head))

	select {
	case <-stream.Sent():
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive response after head event")
	}
	response := stream.ResponseAt(0)
	assert.True(t, response.FullResponse)
	require.Len(t, response.GetChainDescription().GetChainEvent(), 7)
	headEvent := response.GetChainDescription().GetChainEvent()[3].GetHead()
	require.NotNil(t, headEvent)
	assert.Equal(t, uint64(100), headEvent.Height)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_SubscribesAddedChainSupervisor(t *testing.T) {
	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	head := protocol.NewBlockWithHeight(321)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))
	manager.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: chainSupervisor})

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)
	assert.True(t, stream.ResponseAt(0).FullResponse)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_SendsDetailedFullResponseEvents(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	state := newChainState(chains.ARBITRUM, head, []string{"eth_call", "eth_getBalance"}, protocol.WsCap)
	state.Blocks[protocol.FinalizedBlock] = protocol.NewBlockWithHeight(120)
	state.LowerBounds[protocol.StateBound] = protocol.NewLowerBoundData(50, 1000, protocol.StateBound)
	state.ChainLabels = []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, map[string]string{"region": "us-east-1"}),
	}
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, state)

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)

	response := stream.ResponseAt(0)
	require.NotNil(t, response.ChainDescription)
	require.Len(t, response.ChainDescription.ChainEvent, 7)

	statusEvent := response.ChainDescription.ChainEvent[0].GetStatus()
	require.NotNil(t, statusEvent)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_OK, statusEvent.Availability)

	methodsEvent := response.ChainDescription.ChainEvent[1].GetSupportedMethodsEvent()
	require.NotNil(t, methodsEvent)
	assert.ElementsMatch(t, []string{"eth_call", "eth_getBalance"}, methodsEvent.Methods)

	lowerBoundsEvent := response.ChainDescription.ChainEvent[2].GetLowerBoundsEvent()
	require.NotNil(t, lowerBoundsEvent)
	require.Len(t, lowerBoundsEvent.LowerBounds, 1)
	assert.Equal(t, dshackle.LowerBoundType_LOWER_BOUND_STATE, lowerBoundsEvent.LowerBounds[0].LowerBoundType)
	assert.Equal(t, uint64(50), lowerBoundsEvent.LowerBounds[0].LowerBoundValue)
	assert.Equal(t, uint64(1000), lowerBoundsEvent.LowerBounds[0].LowerBoundTimestamp)

	headEvent := response.ChainDescription.ChainEvent[3].GetHead()
	require.NotNil(t, headEvent)
	assert.Equal(t, uint64(123), headEvent.Height)

	finalizationEvent := response.ChainDescription.ChainEvent[4].GetFinalizationDataEvent()
	require.NotNil(t, finalizationEvent)
	require.Len(t, finalizationEvent.FinalizationData, 1)
	assert.Equal(t, uint64(120), finalizationEvent.FinalizationData[0].Height)
	assert.Equal(t, dshackle.FinalizationType_FINALIZATION_FINALIZED_BLOCK, finalizationEvent.FinalizationData[0].Type)

	supportedSubsEvent := response.ChainDescription.ChainEvent[5].GetSupportedSubscriptionsEvent()
	require.NotNil(t, supportedSubsEvent)
	assert.ElementsMatch(
		t,
		specs.GetSubMethods(chains.GetMethodSpecNameByChain(chains.ARBITRUM)).ToSlice(),
		supportedSubsEvent.Subs,
	)

	nodesEvent := response.ChainDescription.ChainEvent[6].GetNodesEvent()
	require.NotNil(t, nodesEvent)
	require.Len(t, nodesEvent.Nodes, 1)
	assert.Equal(t, uint32(1), nodesEvent.Nodes[0].Quorum)
	assert.ElementsMatch(t, []*dshackle.Label{
		{Name: "region", Value: "us-east-1"},
	}, nodesEvent.Nodes[0].Labels)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_SendsIncrementalEventsForStateChanges(t *testing.T) {
	loadMethodSpecs(t)

	initialHead := protocol.NewBlockWithHeight(100)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, initialHead, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)

	updatedState := newChainState(chains.ARBITRUM, initialHead, []string{"eth_call", "eth_getBalance"}, protocol.WsCap)
	updatedState.Blocks[protocol.FinalizedBlock] = protocol.NewBlockWithHeight(88)
	updatedState.LowerBounds[protocol.StateBound] = protocol.NewLowerBoundData(40, 900, protocol.StateBound)
	updatedState.ChainLabels = []upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(1, map[string]string{"region": "eu-west-1"}),
	}
	chainSupervisor.SetState(updatedState)
	chainSupervisor.PublishStateEvent(
		upstreams.NewMethodsWrapper([]string{"eth_call", "eth_getBalance"}),
		upstreams.NewBlocksWrapper(updatedState.Blocks),
		upstreams.NewLowerBoundsWrapper([]protocol.LowerBoundData{updatedState.LowerBounds[protocol.StateBound]}),
		upstreams.NewLabelsWrapper(updatedState.ChainLabels),
		upstreams.NewSubMethodsWrapper(updatedState.SubMethods.ToSlice()),
	)

	require.Eventually(t, func() bool {
		return stream.Count() == 2
	}, time.Second, 10*time.Millisecond)

	incrementalStateResponse := stream.ResponseAt(1)
	assert.False(t, incrementalStateResponse.FullResponse)
	assert.Nil(t, incrementalStateResponse.BuildInfo)
	require.Len(t, incrementalStateResponse.ChainDescription.ChainEvent, 5)

	methodsEvent := incrementalStateResponse.ChainDescription.ChainEvent[0].GetSupportedMethodsEvent()
	require.NotNil(t, methodsEvent)
	assert.ElementsMatch(t, []string{"eth_call", "eth_getBalance"}, methodsEvent.Methods)

	finalizationEvent := incrementalStateResponse.ChainDescription.ChainEvent[1].GetFinalizationDataEvent()
	require.NotNil(t, finalizationEvent)
	require.Len(t, finalizationEvent.FinalizationData, 1)
	assert.Equal(t, uint64(88), finalizationEvent.FinalizationData[0].Height)

	lowerBoundsEvent := incrementalStateResponse.ChainDescription.ChainEvent[2].GetLowerBoundsEvent()
	require.NotNil(t, lowerBoundsEvent)
	require.Len(t, lowerBoundsEvent.LowerBounds, 1)
	assert.Equal(t, uint64(40), lowerBoundsEvent.LowerBounds[0].LowerBoundValue)

	nodesEvent := incrementalStateResponse.ChainDescription.ChainEvent[3].GetNodesEvent()
	require.NotNil(t, nodesEvent)
	require.Len(t, nodesEvent.Nodes, 1)
	assert.ElementsMatch(t, []*dshackle.Label{
		{Name: "region", Value: "eu-west-1"},
	}, nodesEvent.Nodes[0].Labels)

	supportedSubsEvent := incrementalStateResponse.ChainDescription.ChainEvent[4].GetSupportedSubscriptionsEvent()
	require.NotNil(t, supportedSubsEvent)
	assert.ElementsMatch(
		t,
		specs.GetSubMethods(chains.GetMethodSpecNameByChain(chains.ARBITRUM)).ToSlice(),
		supportedSubsEvent.Subs,
	)

	nextHead := protocol.NewBlockWithHeight(101)
	nextState := updatedState
	nextState.HeadData = upstreams.NewChainHeadData(nextHead, "up-1")
	chainSupervisor.SetState(nextState)
	chainSupervisor.PublishStateEvent(upstreams.NewHeadWrapper(nextHead))

	require.Eventually(t, func() bool {
		return stream.Count() == 3
	}, time.Second, 10*time.Millisecond)

	incrementalHeadResponse := stream.ResponseAt(2)
	require.Len(t, incrementalHeadResponse.ChainDescription.ChainEvent, 1)
	headEvent := incrementalHeadResponse.ChainDescription.ChainEvent[0].GetHead()
	require.NotNil(t, headEvent)
	assert.Equal(t, uint64(101), headEvent.Height)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_IgnoresNilAddedChainSupervisor(t *testing.T) {
	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()
	time.Sleep(50 * time.Millisecond)

	manager.Publish(&upstreams.AddChainSupervisorEvent{})

	assert.Never(t, func() bool {
		return stream.Count() > 0
	}, 100*time.Millisecond, 10*time.Millisecond)

	stream.cancel()
	require.NoError(t, <-done)
}

func TestSubscribeChainStatus_DoesNotSubscribeSameChainTwice(t *testing.T) {
	head := protocol.NewBlockWithHeight(321)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatus(upstreamSupervisor, stream)
	}()

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)

	manager.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: chainSupervisor})
	nextHead := protocol.NewBlockWithHeight(322)
	nextState := newChainState(chains.ARBITRUM, nextHead, []string{"eth_call"})
	chainSupervisor.SetState(nextState)
	chainSupervisor.PublishStateEvent(upstreams.NewHeadWrapper(nextHead))

	require.Eventually(t, func() bool {
		return stream.Count() == 2
	}, time.Second, 10*time.Millisecond)
	assert.Never(t, func() bool {
		return stream.Count() > 2
	}, 100*time.Millisecond, 10*time.Millisecond)

	stream.cancel()
	require.NoError(t, <-done)
}
