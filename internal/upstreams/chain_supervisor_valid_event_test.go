package upstreams_test

import (
	"context"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createValidEvent(id string, status protocol.AvailabilityStatus, head protocol.Block, methods *mocks.MethodsMock) protocol.UpstreamEvent {
	state := protocol.DefaultUpstreamState(
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"",
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = head

	return protocol.UpstreamEvent{
		Id:        id,
		EventType: &protocol.ValidUpstreamEvent{State: &state},
	}
}

// A ValidUpstreamEvent must restore a previously removed upstream on its own.
// Before this behavior existed, a removed-then-recovered upstream re-entered
// the chain map only when a later StateUpstreamEvent happened to fire, which
// requires some sub-state to actually change - a node that comes back
// identical to how it left would never be re-added.
func TestChainSupervisorValidUpstreamEventRestoresRemovedUpstream(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	go chainSupervisor.Start()

	head := protocol.NewBlockWithHeight(100)
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent("id", protocol.Available, head, methodsMock))
	publishHeadEvent(chainSupervisor, "id", protocol.Available, head)
	assertEventuallyEqual(t, protocol.Available, func() any { return chainSupervisor.GetChainState().Status })

	chainSupervisor.PublishUpstreamEvent(test_utils.CreateRemoveEvent("id"))
	assertEventuallyEqual(t, protocol.Unavailable, func() any { return chainSupervisor.GetChainState().Status })
	require.Nil(t, chainSupervisor.GetUpstreamState("id"))

	// the recovery delta must be observable by stream subscribers
	sub := chainSupervisor.SubscribeState("valid-event-test")
	defer sub.Unsubscribe()

	chainSupervisor.PublishUpstreamEvent(createValidEvent("id", protocol.Available, head, methodsMock))

	assertEventuallyEqual(t, protocol.Available, func() any { return chainSupervisor.GetChainState().Status })
	require.NotNil(t, chainSupervisor.GetUpstreamState("id"))
	assert.Equal(t, protocol.Available, chainSupervisor.GetUpstreamState("id").Status)
	assertEventuallyEqual(t, head, func() any { return chainSupervisor.GetChainState().HeadData.Head })

	statusRestored := func() bool {
		for {
			select {
			case event := <-sub.Events:
				for _, wrapper := range event.Wrappers {
					if statusWrapper, ok := wrapper.(*upstreams.StatusWrapper); ok && statusWrapper.Status == protocol.Available {
						return true
					}
				}
			default:
				return false
			}
		}
	}
	assert.Eventually(t, statusRestored, eventuallyWait, eventuallyTick,
		"subscribers must receive a StatusWrapper(Available) delta on recovery")
}

// A ValidUpstreamEvent without a state snapshot (defensive case) must not
// panic or register a nil state.
func TestChainSupervisorValidUpstreamEventWithoutStateIsIgnored(t *testing.T) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "id",
		EventType: &protocol.ValidUpstreamEvent{},
	})

	assert.Never(t, func() bool {
		return chainSupervisor.GetUpstreamState("id") != nil
	}, 100*eventuallyTick, eventuallyTick)
}
