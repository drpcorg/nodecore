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

func TestIsLogsRequest(t *testing.T) {
	logs := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["logs",{}]`)}, true, "eth")
	logsNoObj := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["logs"]`)}, true, "eth")
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")
	other := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call", Params: []byte(`[]`)}, false, "eth")

	assert.True(t, isLogsRequest(logs))
	assert.True(t, isLogsRequest(logsNoObj))
	assert.False(t, isLogsRequest(newHeads))
	assert.False(t, isLogsRequest(other))
}

func TestLocalLogsAvailable(t *testing.T) {
	supNil := mocks.NewUpstreamSupervisorMock()
	supNil.On("GetChainSupervisor", chains.ETHEREUM).Return(nil)
	assert.False(t, localLogsAvailable(chains.ETHEREUM, supNil))

	// ws head connector without eth_getLogs → NewHeadsCap but no LogsCap
	supHeads := mocks.NewUpstreamSupervisorMock()
	supHeads.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap)},
	})
	assert.False(t, localLogsAvailable(chains.ETHEREUM, supHeads))

	// ws head connector with eth_getLogs → LogsCap → local synthesis
	supLogs := mocks.NewUpstreamSupervisorMock()
	supLogs.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap)},
	})
	assert.True(t, localLogsAvailable(chains.ETHEREUM, supLogs))
}

func logsRequest(params string) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(params)}, true, "eth")
}

func TestParseLogFilterAndMatches(t *testing.T) {
	logAB := []byte(`{"address":"0xABC","topics":["0xaaa","0xddd","0xC"]}`)
	logOther := []byte(`{"address":"0xdef","topics":["0xaaa"]}`)
	logTwoTopics := []byte(`{"address":"0xABC","topics":["0xaaa","0xddd"]}`)

	tests := []struct {
		name   string
		params string
		log    []byte
		match  bool
	}{
		{"empty object matches all", `["logs",{}]`, logAB, true},
		{"no filter object matches all", `["logs"]`, logAB, true},
		{"address string match (case-insensitive)", `["logs",{"address":"0xabc"}]`, logAB, true},
		{"address string mismatch", `["logs",{"address":"0xabc"}]`, logOther, false},
		{"address array match", `["logs",{"address":["0x111","0xabc"]}]`, logAB, true},
		{"address array mismatch", `["logs",{"address":["0x111","0x222"]}]`, logAB, false},
		{"topic exact match", `["logs",{"topics":["0xaaa"]}]`, logAB, true},
		{"topic null is wildcard", `["logs",{"topics":[null,"0xddd"]}]`, logAB, true},
		{"topic OR-set match (case-insensitive)", `["logs",{"topics":[null,null,["0xb","0xc"]]}]`, logAB, true},
		{"topic mismatch", `["logs",{"topics":["0xzzz"]}]`, logAB, false},
		{"more filter topics than log has -> reject", `["logs",{"topics":["0xaaa","0xddd","0xc"]}]`, logTwoTopics, false},
		{"address+topics combined", `["logs",{"address":"0xabc","topics":["0xaaa"]}]`, logAB, true},

		// [topic1, null, topic2] — exact at pos0, wildcard at pos1, exact at pos2.
		{"[t1,null,t2] all positions satisfied", `["logs",{"topics":["0xaaa",null,"0xc"]}]`, logAB, true},
		{"[t1,null,t2] pos0 mismatch", `["logs",{"topics":["0xzzz",null,"0xc"]}]`, logAB, false},
		{"[t1,null,t2] pos2 mismatch", `["logs",{"topics":["0xaaa",null,"0xzzz"]}]`, logAB, false},
		{"[t1,null,t2] but log too short", `["logs",{"topics":["0xaaa",null,"0xc"]}]`, logTwoTopics, false},

		// [topic1, [topic2, topic3]] — exact at pos0, OR-set at pos1.
		{"[t1,[a,b]] OR-set hit", `["logs",{"topics":["0xaaa",["0xddd","0xeee"]]}]`, logAB, true},
		{"[t1,[a,b]] OR-set miss", `["logs",{"topics":["0xaaa",["0xfff","0xeee"]]}]`, logAB, false},
		{"[t1,[a,b]] pos0 mismatch with OR-set pos1", `["logs",{"topics":["0xzzz",["0xddd","0xeee"]]}]`, logAB, false},

		// OR-set / null at the first position.
		{"OR-set at pos0 hit", `["logs",{"topics":[["0xaaa","0xbbb"]]}]`, logAB, true},
		{"OR-set at pos0 miss", `["logs",{"topics":[["0xbbb","0xccc"]]}]`, logAB, false},
		{"all-null positions match all", `["logs",{"topics":[null,null]}]`, logAB, true},

		// Degenerate topic positions are treated as wildcards.
		{"empty topics array matches all", `["logs",{"topics":[]}]`, logAB, true},
		{"empty OR-set is wildcard", `["logs",{"topics":[[]]}]`, logAB, true},

		// A wildcard trailing position never imposes a length requirement.
		{"trailing null past log length still matches", `["logs",{"topics":["0xaaa","0xddd","0xc",null]}]`, logAB, true},
		// ...but a concrete trailing position past log length rejects.
		{"trailing concrete topic past log length rejects", `["logs",{"topics":[null,null,null,"0xc"]}]`, logAB, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := parseLogFilter(logsRequest(tc.params))
			assert.NoError(t, err)
			assert.Equal(t, tc.match, filter.Matches(parseLogEvent(tc.log)))
		})
	}
}

func TestParseLogFilterMalformedAddress(t *testing.T) {
	// A present-but-malformed address must be rejected (error -> generic
	// node-backed path) rather than silently matching every address (firehose).
	malformed := []string{
		`["logs",{"address":123}]`,
		`["logs",{"address":{"foo":"bar"}}]`,
		`["logs",{"address":[1,2,3]}]`,
		`["logs",{"address":true}]`,
	}
	for _, params := range malformed {
		t.Run(params, func(t *testing.T) {
			_, err := parseLogFilter(logsRequest(params))
			assert.Error(t, err)
		})
	}

	// Absent address must NOT error and must match any address.
	filter, err := parseLogFilter(logsRequest(`["logs",{"topics":["0xaaa"]}]`))
	assert.NoError(t, err)
	assert.True(t, filter.Matches(parseLogEvent([]byte(`{"address":"0xanything","topics":["0xaaa"]}`))))
}

func TestParseLogEvent(t *testing.T) {
	// address and topics are lowercased; raw is preserved verbatim.
	raw := []byte(`{"address":"0xABC","topics":["0xAAA","0xDdD"],"removed":false}`)
	pl := parseLogEvent(raw)
	assert.Equal(t, "0xabc", pl.address)
	assert.Equal(t, []string{"0xaaa", "0xddd"}, pl.topics)
	assert.Equal(t, raw, []byte(pl.Raw()))

	// Missing fields stay zero-valued (no panic): empty address, no topics.
	plEmpty := parseLogEvent([]byte(`{"data":"0x0"}`))
	assert.Equal(t, "", plEmpty.address)
	assert.Empty(t, plEmpty.topics)
}

func TestSubscriptionKeyDependsOnMethodParamsAndSelectors(t *testing.T) {
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")
	logs := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["logs"]`)}, true, "eth")
	newHeadsAgain := protocol.NewUpstreamJsonRpcRequest("2", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")

	assert.NotEqual(t, subscriptionKey(newHeads), subscriptionKey(logs))
	// same method+params (different client id) collapse onto the same source
	assert.Equal(t, subscriptionKey(newHeads), subscriptionKey(newHeadsAgain))
}
