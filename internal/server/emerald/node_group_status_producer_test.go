package emerald_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type recordingGroupStatusStream struct {
	*subscribeChainStatusStream
	mu        sync.Mutex
	responses []*dshackle.SubscribeNodeGroupStatusResponse
	sentAt    []time.Time
	// when set, every Send waits for a value
	gate chan struct{}
}

func (s *recordingGroupStatusStream) Send(resp *dshackle.SubscribeNodeGroupStatusResponse) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.ctx.Done():
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, proto.Clone(resp).(*dshackle.SubscribeNodeGroupStatusResponse))
	s.sentAt = append(s.sentAt, time.Now())
	return nil
}

func (s *recordingGroupStatusStream) sendTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sentAt)
}

func (s *recordingGroupStatusStream) all() []*dshackle.SubscribeNodeGroupStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.responses)
}

func (s *recordingGroupStatusStream) waitFor(t *testing.T, count int) []*dshackle.SubscribeNodeGroupStatusResponse {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.all()) >= count }, time.Second, time.Millisecond)
	return s.all()
}

// quiet asserts that nothing more is sent for a while.
func (s *recordingGroupStatusStream) quiet(t *testing.T, count int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	require.Len(t, s.all(), count)
}

func startRecordedGroupStatus(
	t *testing.T,
	request *dshackle.SubscribeNodeGroupStatusRequest,
	resyncInterval time.Duration,
	chainSupervisors ...upstreams.ChainSupervisor,
) (*recordingGroupStatusStream, *chainsUpstreamSupervisor) {
	t.Helper()
	return startRecordedGroupStatusWithInterval(t, request, time.Millisecond, resyncInterval, chainSupervisors...)
}

func startRecordedGroupStatusWithInterval(
	t *testing.T,
	request *dshackle.SubscribeNodeGroupStatusRequest,
	interval time.Duration,
	resyncInterval time.Duration,
	chainSupervisors ...upstreams.ChainSupervisor,
) (*recordingGroupStatusStream, *chainsUpstreamSupervisor) {
	t.Helper()
	loadMethodSpecs(t)

	supervisor := newChainsUpstreamSupervisor(chainSupervisors...)
	stream := &recordingGroupStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeNodeGroupStatusWithResync(supervisor, request, stream, interval, resyncInterval)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	return stream, supervisor
}

func TestSubscribeNodeGroupStatus_ChainsFilter(t *testing.T) {
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	request := &dshackle.SubscribeNodeGroupStatusRequest{
		// an unknown ref is ignored
		Chains: []dshackle.ChainRef{chainRef(chains.POLYGON), dshackle.ChainRef(999999)},
	}
	stream, supervisor := startRecordedGroupStatus(t, request, time.Hour, ethereum)
	stream.quiet(t, 0)

	// a chain added after subscribe
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	supervisor.add(polygon)

	response := stream.waitFor(t, 1)[0]
	assert.Equal(t, chainRef(chains.POLYGON), response.Chain)
	require.Len(t, response.Groups, 1)
	assert.Contains(t, response.Groups[0].UpstreamIndices, "polygon-1")

	ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(101) })
	stream.quiet(t, 1)
}

func TestSubscribeNodeGroupStatus_EmptyFilterStreamsEveryChain(t *testing.T) {
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	stream, _ := startRecordedGroupStatus(t, &dshackle.SubscribeNodeGroupStatusRequest{}, time.Hour, ethereum, polygon)

	responses := stream.waitFor(t, 2)
	assert.ElementsMatch(t,
		[]dshackle.ChainRef{chainRef(chains.ETHEREUM), chainRef(chains.POLYGON)},
		[]dshackle.ChainRef{responses[0].Chain, responses[1].Chain},
	)
}

func TestSubscribeNodeGroupStatus_SlowConsumerGetsTheLatestState(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))

	loadMethodSpecs(t)
	supervisor := newChainsUpstreamSupervisor(chainSupervisor)
	stream := &recordingGroupStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream(), gate: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeNodeGroupStatusWithResync(supervisor, &dshackle.SubscribeNodeGroupStatusRequest{}, stream, time.Millisecond, time.Hour)
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
	assert.Equal(t, uint64(100), responses[0].Groups[0].Head.Height)
	assert.Equal(t, uint64(105), responses[1].Groups[0].Head.Height)
	stream.quiet(t, 2)
}

func TestSubscribeNodeGroupStatus_StopsOnContextCancel(t *testing.T) {
	loadMethodSpecs(t)
	supervisor := newChainsUpstreamSupervisor()
	stream := &recordingGroupStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeNodeGroupStatusWithResync(supervisor, &dshackle.SubscribeNodeGroupStatusRequest{}, stream, time.Millisecond, time.Hour)
	}()

	stream.cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("the stream did not stop")
	}
}

// a change is sent at once, not on the next poll, once the gap to the last
// send of the chain is over
func TestSubscribeNodeGroupStatus_WakesOnChange(t *testing.T) {
	const interval = 300 * time.Millisecond
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	stream, _ := startRecordedGroupStatusWithInterval(t, &dshackle.SubscribeNodeGroupStatusRequest{}, interval, time.Hour, chainSupervisor)
	stream.waitFor(t, 1)

	for height := uint64(101); height <= 103; height++ {
		time.Sleep(interval + 20*time.Millisecond)
		changedAt := time.Now()
		chainSupervisor.update("up-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(height) })
		count := int(height - 99)
		response := stream.waitFor(t, count)[count-1]
		assert.Equal(t, height, response.Groups[0].Head.Height)
		assert.Less(t, stream.sendTimes()[count-1].Sub(changedAt), 50*time.Millisecond)
	}
}

func TestSubscribeNodeGroupStatus_IntervalIsTheMinimumGapOfAChain(t *testing.T) {
	const interval = 200 * time.Millisecond
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	stream, _ := startRecordedGroupStatusWithInterval(t, &dshackle.SubscribeNodeGroupStatusRequest{}, interval, time.Hour, ethereum, polygon)
	fulls := stream.waitFor(t, 2)
	ethereumFull := slices.IndexFunc(fulls, func(r *dshackle.SubscribeNodeGroupStatusResponse) bool { return r.Chain == chainRef(chains.ETHEREUM) })

	// a burst within the gap is one response, with the latest state
	for height := uint64(101); height <= 105; height++ {
		ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(height) })
	}
	response := stream.waitFor(t, 3)[2]
	assert.Equal(t, chainRef(chains.ETHEREUM), response.Chain)
	assert.Equal(t, uint64(105), response.Groups[0].Head.Height)

	// the gap is per chain: polygon goes out while ethereum waits
	ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(106) })
	polygon.update("polygon-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(201) })
	responses := stream.waitFor(t, 5)
	stream.quiet(t, 5)
	assert.Equal(t, chainRef(chains.POLYGON), responses[3].Chain)
	assert.Equal(t, uint64(201), responses[3].Groups[0].Head.Height)
	assert.Equal(t, chainRef(chains.ETHEREUM), responses[4].Chain)
	assert.Equal(t, uint64(106), responses[4].Groups[0].Head.Height)

	sentAt := stream.sendTimes()
	assert.GreaterOrEqual(t, sentAt[2].Sub(sentAt[ethereumFull]), interval)
	assert.Less(t, sentAt[3].Sub(sentAt[2]), interval/2)
	assert.GreaterOrEqual(t, sentAt[4].Sub(sentAt[2]), interval)
}

// an idle chain is not looked at: only a change wakes its sender
func TestSubscribeNodeGroupStatus_IdleChainCostsNothing(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))
	stream, _ := startRecordedGroupStatus(t, &dshackle.SubscribeNodeGroupStatusRequest{}, time.Hour, chainSupervisor)
	stream.waitFor(t, 1)

	reads := chainSupervisor.reads.Load()
	stream.quiet(t, 1)
	assert.Equal(t, reads, chainSupervisor.reads.Load())
}
