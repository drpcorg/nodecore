package emerald_test

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type upstreamStatusStream struct {
	*subscribeChainStatusStream
	mu        sync.Mutex
	responses []*dshackle.SubscribeUpstreamStatusResponse
	// when set, every Send waits for a value
	gate chan struct{}
}

func (s *upstreamStatusStream) Send(resp *dshackle.SubscribeUpstreamStatusResponse) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.ctx.Done():
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, proto.Clone(resp).(*dshackle.SubscribeUpstreamStatusResponse))
	return nil
}

func (s *upstreamStatusStream) all() []*dshackle.SubscribeUpstreamStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.responses)
}

func (s *upstreamStatusStream) waitFor(t *testing.T, count int) []*dshackle.SubscribeUpstreamStatusResponse {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.all()) >= count }, time.Second, time.Millisecond)
	return s.all()
}

// quiet asserts that nothing more is sent for a while.
func (s *upstreamStatusStream) quiet(t *testing.T, count int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	require.Len(t, s.all(), count)
}

type upstreamsChainSupervisor struct {
	*fakeChainSupervisor
	mu     sync.RWMutex
	states map[string]*protocol.UpstreamState
}

func newUpstreamsChainSupervisor(chain chains.Chain) *upstreamsChainSupervisor {
	return &upstreamsChainSupervisor{
		fakeChainSupervisor: newFakeChainSupervisor(chain, upstreams.ChainSupervisorState{}),
		states:              make(map[string]*protocol.UpstreamState),
	}
}

func (s *upstreamsChainSupervisor) GetUpstreamIds() []string {
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
	defer s.mu.Unlock()
	s.states[id] = state
}

func (s *upstreamsChainSupervisor) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, id)
}

// update stores a copy of the upstream's snapshot, the way the supervisor does
func (s *upstreamsChainSupervisor) update(id string, change func(*protocol.UpstreamState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := *s.states[id]
	change(&next)
	s.states[id] = &next
}

type chainsUpstreamSupervisor struct {
	*mocks.UpstreamSupervisorMock
	mu     sync.Mutex
	chains []upstreams.ChainSupervisor
}

func (s *chainsUpstreamSupervisor) GetChainSupervisors() []upstreams.ChainSupervisor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.chains)
}

func (s *chainsUpstreamSupervisor) add(chainSupervisor upstreams.ChainSupervisor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains = append(s.chains, chainSupervisor)
}

func startUpstreamStatus(
	t *testing.T,
	request *dshackle.SubscribeUpstreamStatusRequest,
	resyncInterval time.Duration,
	chainSupervisors ...upstreams.ChainSupervisor,
) (*upstreamStatusStream, *chainsUpstreamSupervisor) {
	t.Helper()
	loadMethodSpecs(t)

	supervisor := &chainsUpstreamSupervisor{UpstreamSupervisorMock: mocks.NewUpstreamSupervisorMock(), chains: chainSupervisors}
	stream := &upstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(supervisor, request, stream, time.Millisecond, resyncInterval)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	return stream, supervisor
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

func upstreamIds(response *dshackle.SubscribeUpstreamStatusResponse) []string {
	ids := make([]string, 0, len(response.Upstreams))
	for _, up := range response.Upstreams {
		ids = append(ids, up.UpstreamId)
	}
	return ids
}

// described lists the upstreams a response carries a description for.
func described(response *dshackle.SubscribeUpstreamStatusResponse) []string {
	ids := make([]string, 0)
	for _, up := range response.Upstreams {
		if len(up.Description) > 0 {
			ids = append(ids, up.UpstreamId)
		}
	}
	return ids
}

func TestSubscribeUpstreamStatus_NilSupervisorReturnsError(t *testing.T) {
	err := emerald.SubscribeUpstreamStatus(nil, &dshackle.SubscribeUpstreamStatusRequest{}, nil, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, "upstream supervisor cannot be nil", err.Error())
}

func TestSubscribeUpstreamStatus_FirstResponseIsFull(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100, "eth_call"))
	syncing := testUpstreamState(90, "eth_call", "eth_getLogs")
	syncing.Status = protocol.Syncing
	chainSupervisor.set("up-2", syncing)

	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)

	response := stream.waitFor(t, 1)[0]
	assert.True(t, response.FullResponse)
	require.NotNil(t, response.BuildInfo)
	assert.True(t, strings.HasPrefix(response.BuildInfo.Version, "nodecore/"))
	assert.Equal(t, chainRef(chains.ETHEREUM), response.Chain)
	require.Equal(t, []string{"up-1", "up-2"}, upstreamIds(response))
	assert.Equal(t, []string{"up-1", "up-2"}, described(response))

	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_OK, response.Upstreams[0].Status.Availability)
	assert.Equal(t, uint64(100), response.Upstreams[0].Head.Height)
	// an upstream is described whatever its status
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, response.Upstreams[1].Status.Availability)
	assert.Equal(t, uint64(90), response.Upstreams[1].Head.Height)
	assert.ElementsMatch(t, []string{"eth_call", "eth_getLogs"}, response.Upstreams[1].Description[0].GetSupportedMethodsEvent().Methods)

	stream.quiet(t, 1)
}

func TestSubscribeUpstreamStatus_DescribesOneUpstream(t *testing.T) {
	methodsMock := newMethodsMockWithSupported("eth_call", "eth_subscribe")
	methodsMock.On("GetMethod", "eth_subscribe").Return(specs.GetSpecMethod("eth", "eth_subscribe"))
	methodsMock.On("GetMethod", mock.Anything).Return(nil)
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap), "", nil, nil)
	state.Status = protocol.Unavailable
	state.HeadData = protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("0x01"), blockchain.NewHashIdFromString("0x02"))
	state.LowerBoundsInfo.AddLowerBound(protocol.NewLowerBoundData(5, 10, protocol.StateBound))
	state.BlockInfo.AddBlock(protocol.NewBlockWithHeight(95), protocol.FinalizedBlock)
	state.Labels.AddLabel("client_type", "geth")

	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", &state)
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)

	upstream := stream.waitFor(t, 1)[0].Upstreams[0]
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, upstream.Status.Availability)
	assert.Equal(t, uint64(100), upstream.Head.Height)
	assert.Equal(t, "01", upstream.Head.BlockId)
	assert.Equal(t, "02", upstream.Head.ParentBlockId)

	require.Len(t, upstream.Description, 5)
	assert.ElementsMatch(t, []string{"eth_call", "eth_subscribe"}, upstream.Description[0].GetSupportedMethodsEvent().Methods)
	bounds := upstream.Description[1].GetLowerBoundsEvent().LowerBounds
	require.Len(t, bounds, 1)
	assert.Equal(t, dshackle.LowerBoundType_LOWER_BOUND_STATE, bounds[0].LowerBoundType)
	assert.Equal(t, uint64(5), bounds[0].LowerBoundValue)
	finalization := upstream.Description[2].GetFinalizationDataEvent().FinalizationData
	require.Len(t, finalization, 1)
	assert.Equal(t, uint64(95), finalization[0].Height)
	// the merged rule: eth_subscribe becomes the topics the caps allow
	assert.Equal(t, []string{"newHeads"}, upstream.Description[3].GetSupportedSubscriptionsEvent().Subs)
	nodes := upstream.Description[4].GetNodesEvent().Nodes
	require.Len(t, nodes, 1)
	assert.Equal(t, uint32(1), nodes[0].Quorum)
	assert.Equal(t, []*dshackle.Label{{Name: "client_type", Value: "geth"}}, nodes[0].Labels)
}

func TestSubscribeUpstreamStatus_UnlabeledUpstreamIsOneNode(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)

	nodes := stream.waitFor(t, 1)[0].Upstreams[0].Description[4].GetNodesEvent().Nodes
	require.Len(t, nodes, 1)
	assert.Equal(t, uint32(1), nodes[0].Quorum)
	assert.Empty(t, nodes[0].Labels)
}

func TestSubscribeUpstreamStatus_FollowsTheChainSupervisor(t *testing.T) {
	loadMethodSpecs(t)
	chainSupervisor := upstreams.NewGenericChainSupervisor(t.Context(), chains.ETHEREUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()
	methodsMock := newMethodsMockWithSupported("eth_call")
	methodsMock.On("GetMethod", mock.Anything).Return(nil)
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("up-1", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock))

	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)
	response := stream.waitFor(t, 1)[0]
	assert.True(t, response.FullResponse)
	assert.Equal(t, uint64(100), response.Upstreams[0].Head.Height)

	// a head keeps the description parts of the snapshot
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "up-1",
		EventType: &protocol.HeadUpstreamEvent{Status: protocol.Available, Head: protocol.NewBlockWithHeight(101)},
	})
	response = stream.waitFor(t, 2)[1]
	assert.Equal(t, uint64(101), response.Upstreams[0].Head.Height)
	assert.Empty(t, described(response))

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("up-1"))
	response = stream.waitFor(t, 3)[2]
	assert.Empty(t, response.Upstreams)
	stream.quiet(t, 3)
}

func TestSubscribeUpstreamStatus_ChainWaitsForItsFirstUpstream(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)
	stream.quiet(t, 0)

	chainSupervisor.set("up-1", testUpstreamState(100))
	response := stream.waitFor(t, 1)[0]
	assert.True(t, response.FullResponse)
	assert.Equal(t, []string{"up-1"}, upstreamIds(response))
}

func TestSubscribeUpstreamStatus_DeltasOnlyWhenChanged(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	chainSupervisor.set("up-2", testUpstreamState(100))
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)
	stream.waitFor(t, 1)
	stream.quiet(t, 1)

	// a new snapshot with nothing the stream carries changed
	chainSupervisor.update("up-1", func(state *protocol.UpstreamState) { state.UpstreamIndex = "other" })
	stream.quiet(t, 1)

	// a head: every upstream listed, none described
	chainSupervisor.update("up-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(101) })
	response := stream.waitFor(t, 2)[1]
	assert.False(t, response.FullResponse)
	assert.Nil(t, response.BuildInfo)
	assert.Equal(t, []string{"up-1", "up-2"}, upstreamIds(response))
	assert.Empty(t, described(response))
	assert.Equal(t, uint64(101), response.Upstreams[0].Head.Height)
	stream.quiet(t, 2)

	// a description part: only that upstream described
	chainSupervisor.update("up-2", func(state *protocol.UpstreamState) {
		labels := state.Labels.Copy()
		labels.AddLabel("archive", "true")
		state.Labels = labels
	})
	response = stream.waitFor(t, 3)[2]
	assert.Equal(t, []string{"up-1", "up-2"}, upstreamIds(response))
	assert.Equal(t, []string{"up-2"}, described(response))

	// a status: described too
	chainSupervisor.update("up-1", func(state *protocol.UpstreamState) { state.Status = protocol.Syncing })
	response = stream.waitFor(t, 4)[3]
	assert.Equal(t, []string{"up-1"}, described(response))
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_SYNCING, response.Upstreams[0].Status.Availability)
	stream.quiet(t, 4)
}

func TestSubscribeUpstreamStatus_AddedAndRemovedUpstreams(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	chainSupervisor.set("up-2", testUpstreamState(100))
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, chainSupervisor)
	stream.waitFor(t, 1)

	chainSupervisor.set("up-3", testUpstreamState(100))
	response := stream.waitFor(t, 2)[1]
	assert.False(t, response.FullResponse)
	assert.Equal(t, []string{"up-1", "up-2", "up-3"}, upstreamIds(response))
	assert.Equal(t, []string{"up-3"}, described(response))

	// removal = missing from the list
	chainSupervisor.remove("up-2")
	response = stream.waitFor(t, 3)[2]
	assert.Equal(t, []string{"up-1", "up-3"}, upstreamIds(response))
	assert.Empty(t, described(response))

	// an emptied chain is sent as an empty list, once
	chainSupervisor.remove("up-1")
	chainSupervisor.remove("up-3")
	response = stream.waitFor(t, 4)[3]
	assert.Equal(t, chainRef(chains.ETHEREUM), response.Chain)
	assert.Empty(t, response.Upstreams)
	stream.quiet(t, 4)

	// an upstream that comes back is described again
	chainSupervisor.set("up-2", testUpstreamState(100))
	response = stream.waitFor(t, 5)[4]
	assert.False(t, response.FullResponse)
	assert.Equal(t, []string{"up-2"}, described(response))
}

func TestSubscribeUpstreamStatus_ResyncSendsFull(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	chainSupervisor.set("up-2", testUpstreamState(100))
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, 50*time.Millisecond, chainSupervisor)

	responses := stream.waitFor(t, 3)
	for _, response := range responses[:3] {
		assert.True(t, response.FullResponse)
		assert.NotNil(t, response.BuildInfo)
		assert.Equal(t, []string{"up-1", "up-2"}, described(response))
	}
}

func TestSubscribeUpstreamStatus_ChainsFilter(t *testing.T) {
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	request := &dshackle.SubscribeUpstreamStatusRequest{
		// an unknown ref is ignored
		Chains: []dshackle.ChainRef{chainRef(chains.POLYGON), dshackle.ChainRef(999999)},
	}
	stream, supervisor := startUpstreamStatus(t, request, time.Hour, ethereum)
	stream.quiet(t, 0)

	// a chain added after subscribe
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	supervisor.add(polygon)

	response := stream.waitFor(t, 1)[0]
	assert.Equal(t, chainRef(chains.POLYGON), response.Chain)
	assert.Equal(t, []string{"polygon-1"}, upstreamIds(response))

	ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(101) })
	stream.quiet(t, 1)
}

func TestSubscribeUpstreamStatus_EmptyFilterStreamsEveryChain(t *testing.T) {
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	stream, _ := startUpstreamStatus(t, &dshackle.SubscribeUpstreamStatusRequest{}, time.Hour, ethereum, polygon)

	responses := stream.waitFor(t, 2)
	assert.ElementsMatch(t,
		[]dshackle.ChainRef{chainRef(chains.ETHEREUM), chainRef(chains.POLYGON)},
		[]dshackle.ChainRef{responses[0].Chain, responses[1].Chain},
	)
}

func TestSubscribeUpstreamStatus_SlowConsumerGetsTheLatestState(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))

	loadMethodSpecs(t)
	supervisor := &chainsUpstreamSupervisor{
		UpstreamSupervisorMock: mocks.NewUpstreamSupervisorMock(),
		chains:                 []upstreams.ChainSupervisor{chainSupervisor},
	}
	stream := &upstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream(), gate: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(supervisor, &dshackle.SubscribeUpstreamStatusRequest{}, stream, time.Millisecond, time.Hour)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})

	// the first Send blocks while the head moves on
	time.Sleep(10 * time.Millisecond)
	for height := uint64(101); height <= 105; height++ {
		chainSupervisor.update("up-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(height) })
		time.Sleep(5 * time.Millisecond)
	}
	go func() {
		for {
			select {
			case stream.gate <- struct{}{}:
			case <-stream.ctx.Done():
				return
			}
		}
	}()

	responses := stream.waitFor(t, 2)
	assert.Equal(t, uint64(100), responses[0].Upstreams[0].Head.Height)
	assert.Equal(t, uint64(105), responses[1].Upstreams[0].Head.Height)
	stream.quiet(t, 2)
}

func TestSubscribeUpstreamStatus_StopsOnContextCancel(t *testing.T) {
	loadMethodSpecs(t)
	supervisor := &chainsUpstreamSupervisor{UpstreamSupervisorMock: mocks.NewUpstreamSupervisorMock()}
	stream := &upstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatus(supervisor, &dshackle.SubscribeUpstreamStatusRequest{}, stream, time.Millisecond)
	}()

	stream.cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("the stream did not stop")
	}
}
