package emerald_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/mock"
)

const testResyncInterval = 50 * time.Millisecond

// The chain-status protocol is delta-based over lossy hops (buffered channel
// fan-outs on both the nodecore and the consumer side drop events under
// pressure), so a single lost delta used to leave a subscriber permanently
// stale - e.g. a consumer kept showing a recovered chain as Unavailable until the
// TCP connection was rebuilt. The periodic state resync repairs any lost
// delta within one interval.
func TestSubscribeChainStatus_PeriodicResyncResendsStateWithoutHead(t *testing.T) {
	loadMethodSpecs(t)

	head := protocol.NewBlockWithHeight(123)
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, head, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatusWithResync(upstreamSupervisor, stream, testResyncInterval)
	}()

	require.Eventually(t, func() bool {
		return stream.Count() == 1
	}, time.Second, 10*time.Millisecond)
	require.True(t, stream.ResponseAt(0).FullResponse)

	// Simulate a lost delta: the state changes but no event reaches the
	// stream. Only the periodic resync can repair the subscriber's view.
	updatedState := newChainState(chains.ARBITRUM, head, []string{"eth_call", "eth_getBalance"})
	updatedState.Status = protocol.Unavailable
	chainSupervisor.SetState(updatedState)

	require.Eventually(t, func() bool {
		return stream.Count() >= 2
	}, time.Second, 10*time.Millisecond)

	resync := stream.ResponseAt(1)
	assert.False(t, resync.FullResponse, "resync must be a regular delta-style response")
	assert.Nil(t, resync.BuildInfo)

	events := resync.GetChainDescription().GetChainEvent()
	require.NotEmpty(t, events)

	var statusEvent *dshackle.ChainStatus
	for _, event := range events {
		require.Nil(t, event.GetHead(),
			"resync must not carry a head: consumers reduce a response with a head to a head-only update, losing the state")
		if event.GetStatus() != nil {
			statusEvent = event.GetStatus()
		}
	}
	require.NotNil(t, statusEvent, "resync must carry the chain status")
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusEvent.Availability,
		"resync must reflect the current state, repairing the lost delta")

	methodsSeen := false
	for _, event := range events {
		if methodsEvent := event.GetSupportedMethodsEvent(); methodsEvent != nil {
			methodsSeen = true
			assert.ElementsMatch(t, []string{"eth_call", "eth_getBalance"}, methodsEvent.Methods)
		}
	}
	assert.True(t, methodsSeen, "resync must carry the supported methods")

	stream.cancel()
	require.NoError(t, <-done)
}

// Before the first full response (no head yet) there is nothing to resync:
// consumers create their per-chain object only from a FullResponse, so a
// state-only snapshot would be silently skipped on its side anyway.
func TestSubscribeChainStatus_NoResyncBeforeFirstFullResponse(t *testing.T) {
	chainSupervisor := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.Block{}, []string{"eth_call"}))

	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return([]upstreams.ChainSupervisor{chainSupervisor})

	stream := newSubscribeChainStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeChainStatusWithResync(upstreamSupervisor, stream, testResyncInterval)
	}()

	assert.Never(t, func() bool {
		return stream.Count() > 0
	}, 5*testResyncInterval, 10*time.Millisecond)

	stream.cancel()
	require.NoError(t, <-done)
}
