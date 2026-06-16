package flow

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

// stubChainSupervisor exposes a fixed ChainSupervisorState for gating tests.
type stubChainSupervisor struct {
	state upstreams.ChainSupervisorState
}

func (s *stubChainSupervisor) Start()                                        {}
func (s *stubChainSupervisor) GetChain() chains.Chain                        { return chains.ETHEREUM }
func (s *stubChainSupervisor) GetChainState() upstreams.ChainSupervisorState { return s.state }
func (s *stubChainSupervisor) GetMethod(string) *specs.Method                { return nil }
func (s *stubChainSupervisor) GetMethods() []string                          { return nil }
func (s *stubChainSupervisor) GetUpstreamState(string) *protocol.UpstreamState {
	return nil
}
func (s *stubChainSupervisor) GetSortedUpstreamIds(upstreams.FilterUpstream, upstreams.SortUpstream) []string {
	return nil
}
func (s *stubChainSupervisor) GetUpstreamIds() []string                    { return nil }
func (s *stubChainSupervisor) PublishUpstreamEvent(protocol.UpstreamEvent) {}
func (s *stubChainSupervisor) SubscribeState(string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return nil
}

var _ upstreams.ChainSupervisor = (*stubChainSupervisor)(nil)

func TestLocalNewHeadsAvailable(t *testing.T) {
	// nil chain supervisor → not available
	supNil := mocks.NewUpstreamSupervisorMock()
	supNil.On("GetChainSupervisor", chains.ETHEREUM).Return(nil)
	assert.False(t, localNewHeadsAvailable(chains.ETHEREUM, supNil))

	// json-rpc head connector → no NewHeadsCap → falls back to generic
	supRpc := mocks.NewUpstreamSupervisorMock()
	supRpc.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap)},
	})
	assert.False(t, localNewHeadsAvailable(chains.ETHEREUM, supRpc))

	// websocket head connector → NewHeadsCap → local synthesis
	supSub := mocks.NewUpstreamSupervisorMock()
	supSub.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap)},
	})
	assert.True(t, localNewHeadsAvailable(chains.ETHEREUM, supSub))
}

func TestSelectorKeyIsOrderIndependent(t *testing.T) {
	a := []protocol.RequestSelector{
		protocol.RequestLabelSelector{Name: "region", Values: []string{"eu", "us"}},
		protocol.RequestExistsSelector{Name: "archive"},
	}
	b := []protocol.RequestSelector{
		protocol.RequestExistsSelector{Name: "archive"},
		protocol.RequestLabelSelector{Name: "region", Values: []string{"us", "eu"}},
	}
	assert.Equal(t, selectorKey(a), selectorKey(b))
}

func TestSelectorKeyNestedGroupsAreOrderIndependent(t *testing.T) {
	a := selectorKey([]protocol.RequestSelector{
		protocol.RequestAndSelector{Children: []protocol.RequestSelector{
			protocol.RequestExistsSelector{Name: "archive"},
			protocol.RequestLabelSelector{Name: "region", Values: []string{"eu", "us"}},
		}},
	})
	b := selectorKey([]protocol.RequestSelector{
		protocol.RequestAndSelector{Children: []protocol.RequestSelector{
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us", "eu"}},
			protocol.RequestExistsSelector{Name: "archive"},
		}},
	})
	assert.Equal(t, a, b)
}

func TestSelectorKeyDistinguishesSelectors(t *testing.T) {
	a := selectorKey([]protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"eu"}}})
	b := selectorKey([]protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}})
	assert.NotEqual(t, a, b)
	assert.Empty(t, selectorKey(nil))
}

func TestIsNewHeadsRequest(t *testing.T) {
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")
	logs := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["logs",{}]`)}, true, "eth")
	other := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call", Params: []byte(`[]`)}, false, "eth")

	assert.True(t, isNewHeadsRequest(newHeads))
	assert.False(t, isNewHeadsRequest(logs))
	assert.False(t, isNewHeadsRequest(other))
}

func TestSubscriptionKeyDependsOnMethodParamsAndSelectors(t *testing.T) {
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")
	logs := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["logs"]`)}, true, "eth")
	newHeadsAgain := protocol.NewUpstreamJsonRpcRequest("2", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")

	assert.NotEqual(t, subscriptionKey(newHeads), subscriptionKey(logs))
	// same method+params (different client id) collapse onto the same source
	assert.Equal(t, subscriptionKey(newHeads), subscriptionKey(newHeadsAgain))
}
