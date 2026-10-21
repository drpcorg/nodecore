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

type recordingUpstreamStatusStream struct {
	*subscribeChainStatusStream
	mu        sync.Mutex
	responses []*dshackle.SubscribeChainStatusResponse
	sentAt    []time.Time
	// when set, every Send waits for a value
	gate chan struct{}
}

func (s *recordingUpstreamStatusStream) Send(resp *dshackle.SubscribeChainStatusResponse) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.ctx.Done():
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, proto.Clone(resp).(*dshackle.SubscribeChainStatusResponse))
	s.sentAt = append(s.sentAt, time.Now())
	return nil
}

func (s *recordingUpstreamStatusStream) sendTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sentAt)
}

func (s *recordingUpstreamStatusStream) all() []*dshackle.SubscribeChainStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.responses)
}

func (s *recordingUpstreamStatusStream) waitFor(t *testing.T, count int) []*dshackle.SubscribeChainStatusResponse {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.all()) >= count }, time.Second, time.Millisecond)
	return s.all()
}

// quiet asserts that nothing more is sent for a while.
func (s *recordingUpstreamStatusStream) quiet(t *testing.T, count int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	require.Len(t, s.all(), count)
}

func startRecordedUpstreamStatus(
	t *testing.T,
	resyncInterval time.Duration,
	chainSupervisors ...upstreams.ChainSupervisor,
) (*recordingUpstreamStatusStream, *chainsUpstreamSupervisor) {
	t.Helper()
	return startRecordedUpstreamStatusWithInterval(t, time.Millisecond, resyncInterval, chainSupervisors...)
}

func startRecordedUpstreamStatusWithInterval(
	t *testing.T,
	interval time.Duration,
	resyncInterval time.Duration,
	chainSupervisors ...upstreams.ChainSupervisor,
) (*recordingUpstreamStatusStream, *chainsUpstreamSupervisor) {
	t.Helper()
	loadMethodSpecs(t)

	supervisor := newChainsUpstreamSupervisor(chainSupervisors...)
	stream := &recordingUpstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(supervisor, stream, interval, resyncInterval)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	return stream, supervisor
}

func TestSubscribeUpstreamStatus_StreamsEveryChain(t *testing.T) {
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	stream, _ := startRecordedUpstreamStatus(t, time.Hour, ethereum, polygon)

	responses := stream.waitFor(t, 2)
	assert.ElementsMatch(t,
		[]dshackle.ChainRef{chainRef(chains.ETHEREUM), chainRef(chains.POLYGON)},
		[]dshackle.ChainRef{responses[0].GetChainDescription().GetChain(), responses[1].GetChainDescription().GetChain()},
	)
}

func TestSubscribeUpstreamStatus_SlowConsumerGetsTheLatestState(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(chains.ETHEREUM)
	chainSupervisor.set("up-1", testUpstreamState(100))

	loadMethodSpecs(t)
	supervisor := newChainsUpstreamSupervisor(chainSupervisor)
	stream := &recordingUpstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream(), gate: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(supervisor, stream, time.Millisecond, time.Hour)
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
	assert.Equal(t, uint64(100), responseHeight(responses[0]))
	assert.Equal(t, uint64(105), responseHeight(responses[1]))
	stream.quiet(t, 2)
}

func TestSubscribeUpstreamStatus_IntervalIsTheMinimumGapOfAChain(t *testing.T) {
	const interval = 200 * time.Millisecond
	ethereum := newUpstreamsChainSupervisor(chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	stream, _ := startRecordedUpstreamStatusWithInterval(t, interval, time.Hour, ethereum, polygon)
	fulls := stream.waitFor(t, 2)
	ethereumFull := slices.IndexFunc(fulls, func(r *dshackle.SubscribeChainStatusResponse) bool {
		return r.GetChainDescription().GetChain() == chainRef(chains.ETHEREUM)
	})

	// a burst within the gap is one response, with the latest state
	for height := uint64(101); height <= 105; height++ {
		ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(height) })
	}
	response := stream.waitFor(t, 3)[2]
	assert.Equal(t, chainRef(chains.ETHEREUM), response.GetChainDescription().GetChain())
	assert.Equal(t, uint64(105), responseHeight(response))

	// the gap is per chain: polygon goes out while ethereum waits
	ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(106) })
	polygon.update("polygon-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(201) })
	responses := stream.waitFor(t, 5)
	stream.quiet(t, 5)
	assert.Equal(t, chainRef(chains.POLYGON), responses[3].GetChainDescription().GetChain())
	assert.Equal(t, uint64(201), responseHeight(responses[3]))
	assert.Equal(t, chainRef(chains.ETHEREUM), responses[4].GetChainDescription().GetChain())
	assert.Equal(t, uint64(106), responseHeight(responses[4]))

	sentAt := stream.sendTimes()
	assert.GreaterOrEqual(t, sentAt[2].Sub(sentAt[ethereumFull]), interval)
	assert.Less(t, sentAt[3].Sub(sentAt[2]), interval/2)
	assert.GreaterOrEqual(t, sentAt[4].Sub(sentAt[2]), interval)
}

func responseHeight(r *dshackle.SubscribeChainStatusResponse) uint64 {
	for _, e := range r.GetChainDescription().GetChainEvent() {
		if e.GetHead() != nil {
			return e.GetHead().GetHeight()
		}
	}
	return 0
}
