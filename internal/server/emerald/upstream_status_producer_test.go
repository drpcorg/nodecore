package emerald_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/signature"
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
	// when set, Send waits until the gate opens
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
	loadMethodSpecs(t)

	supervisor := newChainsUpstreamSupervisor(chainSupervisors...)
	stream := &recordingUpstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(supervisor, stream, time.Millisecond, resyncInterval)
	}()
	t.Cleanup(func() {
		stream.cancel()
		require.NoError(t, <-done)
	})
	return stream, supervisor
}

func TestSubscribeUpstreamStatus_SlowConsumerGetsTheLatestState(t *testing.T) {
	chainSupervisor := newUpstreamsChainSupervisor(t, chains.ETHEREUM)
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
	close(stream.gate)

	require.Eventually(t, func() bool {
		responses := stream.all()
		return len(responses) > 1 && responseHeight(responses[len(responses)-1]) == 105
	}, time.Second, time.Millisecond)
	assert.Equal(t, uint64(100), responseHeight(stream.all()[0]))
}

func TestSubscribeUpstreamStatusRequestCoalescesEachChainAndStreamIndependently(t *testing.T) {
	const interval = 200 * time.Millisecond
	ethereum := newUpstreamsChainSupervisor(t, chains.ETHEREUM)
	ethereum.set("eth-1", testUpstreamState(100))
	polygon := newUpstreamsChainSupervisor(t, chains.POLYGON)
	polygon.set("polygon-1", testUpstreamState(200))
	supervisor := newChainsUpstreamSupervisor(ethereum, polygon)
	service := emerald.NewGrpcBlockchainService(&server_ctx.ApplicationServerContext{UpstreamSupervisor: supervisor}, nil, signature.NewDisabledSigner())
	streams := make([]*recordingUpstreamStatusStream, 2)
	for i, minInterval := range []uint32{0, uint32(interval / time.Millisecond)} {
		stream := &recordingUpstreamStatusStream{subscribeChainStatusStream: newSubscribeChainStatusStream()}
		streams[i] = stream
		done := make(chan error, 1)
		go func() {
			done <- service.SubscribeUpstreamStatus(&dshackle.SubscribeChainStatusRequest{MinIntervalMs: minInterval}, stream)
		}()
		t.Cleanup(func() {
			stream.cancel()
			require.NoError(t, <-done)
		})
		fulls := stream.waitFor(t, 2)
		assert.ElementsMatch(t,
			[]dshackle.ChainRef{chainRef(chains.ETHEREUM), chainRef(chains.POLYGON)},
			[]dshackle.ChainRef{fulls[0].GetChainDescription().GetChain(), fulls[1].GetChainDescription().GetChain()},
		)
		for _, full := range fulls {
			require.True(t, full.FullResponse)
		}
	}
	immediate, delayed := streams[0], streams[1]
	ethereumFull := slices.IndexFunc(delayed.all(), func(r *dshackle.SubscribeChainStatusResponse) bool {
		return r.GetChainDescription().GetChain() == chainRef(chains.ETHEREUM)
	})

	// The same burst is immediate on one stream and coalesced on the other.
	for height := uint64(101); height <= 105; height++ {
		ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(height) })
		response := immediate.waitFor(t, int(height-98))[int(height-99)]
		require.Equal(t, height, responseHeight(response))
	}
	require.Len(t, delayed.all(), 2)
	response := delayed.waitFor(t, 3)[2]
	assert.Equal(t, chainRef(chains.ETHEREUM), response.GetChainDescription().GetChain())
	assert.Equal(t, uint64(105), responseHeight(response))

	// Polygon goes out while ethereum waits for its next gap.
	ethereum.update("eth-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(106) })
	polygon.update("polygon-1", func(state *protocol.UpstreamState) { state.HeadData = protocol.NewBlockWithHeight(201) })
	responses := delayed.waitFor(t, 5)
	delayed.quiet(t, 5)
	assert.Equal(t, chainRef(chains.POLYGON), responses[3].GetChainDescription().GetChain())
	assert.Equal(t, uint64(201), responseHeight(responses[3]))
	assert.Equal(t, chainRef(chains.ETHEREUM), responses[4].GetChainDescription().GetChain())
	assert.Equal(t, uint64(106), responseHeight(responses[4]))

	sentAt := delayed.sendTimes()
	assert.GreaterOrEqual(t, sentAt[2].Sub(sentAt[ethereumFull]), interval-10*time.Millisecond)
	assert.Less(t, sentAt[3].Sub(sentAt[2]), interval/2)
	assert.GreaterOrEqual(t, sentAt[4].Sub(sentAt[2]), interval-10*time.Millisecond)
}

func responseHeight(r *dshackle.SubscribeChainStatusResponse) uint64 {
	for _, e := range r.GetChainDescription().GetChainEvent() {
		if e.GetHead() != nil {
			return e.GetHead().GetHeight()
		}
	}
	return 0
}

func TestSubscribeUpstreamStatusDiscoversNewChainsAfterEmptyStartup(t *testing.T) {
	stream, supervisor := startRecordedUpstreamStatus(t, 25*time.Millisecond)
	chain := newUpstreamsChainSupervisor(t, chains.ETHEREUM)
	chain.set("a", testUpstreamState(100))
	// No Add notification: periodic registry reconciliation repairs a missed one.
	supervisor.chains.Store(chain.GetChain(), chain)
	require.Equal(t, "a", stream.waitFor(t, 1)[0].UpstreamId)
	polygon := newUpstreamsChainSupervisor(t, chains.POLYGON)
	polygon.set("b", testUpstreamState(200))
	supervisor.chains.Store(polygon.GetChain(), polygon)
	supervisor.events.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: polygon})
	require.Eventually(t, func() bool {
		for _, response := range stream.all() {
			if response.UpstreamId == "b" && response.FullResponse {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
}
