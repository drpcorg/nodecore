package flow

import (
	"sort"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stubChainSupervisor exposes a fixed ChainSupervisorState and a fixed set of
// upstream states for gating tests.
type stubChainSupervisor struct {
	state  upstreams.ChainSupervisorState
	states map[string]*protocol.UpstreamState
}

func (s *stubChainSupervisor) Start()                                        {}
func (s *stubChainSupervisor) GetChain() chains.Chain                        { return chains.ETHEREUM }
func (s *stubChainSupervisor) GetChainState() upstreams.ChainSupervisorState { return s.state }
func (s *stubChainSupervisor) GetMethod(string) *specs.Method                { return nil }
func (s *stubChainSupervisor) GetMethods() []string                          { return nil }
func (s *stubChainSupervisor) GetUpstreamState(id string) *protocol.UpstreamState {
	return s.states[id]
}
func (s *stubChainSupervisor) GetSortedUpstreamIds(filter upstreams.FilterUpstream, _ upstreams.SortUpstream) []string {
	ids := make([]string, 0, len(s.states))
	for id, state := range s.states {
		if filter(id, state) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
func (s *stubChainSupervisor) GetUpstreamIds() []string                    { return nil }
func (s *stubChainSupervisor) NextIndex() uint64                           { return 0 }
func (s *stubChainSupervisor) PublishUpstreamEvent(protocol.UpstreamEvent) {}
func (s *stubChainSupervisor) SubscribeState(string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return nil
}
func (s *stubChainSupervisor) SubscribeHead(string, upstreams.FilterUpstream) *upstreams.HeadFeedSubscription {
	return nil
}

func (s *stubChainSupervisor) UpstreamsChanged() <-chan struct{} {
	return nil
}

var _ upstreams.ChainSupervisor = (*stubChainSupervisor)(nil)

// allLocalSubs enables every local subscription type, the default behavior.
var allLocalSubs = config.LocalSubSettings{NewHeads: true, Logs: true, PendingTx: true}

// capsSupervisor reports a chain with one available upstream advertising caps,
// with labels, and the chain-wide Caps union set to the same caps.
func capsSupervisor(labels map[string]string, caps ...protocol.Cap) *mocks.UpstreamSupervisorMock {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("HasMethod", mock.Anything).Return(true).Maybe()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](caps...), "idx", nil, nil)
	state.Status = protocol.Available
	state.HeadData = protocol.Block{Height: 100}
	state.Labels = protocol.NewLabels()
	for k, v := range labels {
		state.Labels.AddLabel(k, v)
	}
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state:  upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](caps...)},
		states: map[string]*protocol.UpstreamState{"up1": &state},
	})
	return sup
}

// unavailableCapsSupervisor is capsSupervisor whose only upstream is Unavailable:
// the chain can serve the topic locally, just not right now.
func unavailableCapsSupervisor(caps ...protocol.Cap) *mocks.UpstreamSupervisorMock {
	sup := capsSupervisor(nil, caps...)
	stub := sup.GetChainSupervisor(chains.ETHEREUM).(*stubChainSupervisor)
	stub.states["up1"].Status = protocol.Unavailable
	return sup
}

// allCapsSupervisor reports a chain capable of every local subscription type.
func allCapsSupervisor() *mocks.UpstreamSupervisorMock {
	return capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap, protocol.PendingTxCap)
}

func subscribeRequest(params string) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(params)}, true, "eth")
}

// TestResolveSourceRespectsLocalSubSettings verifies the per-chain config gates
// the three node-backed-equivalent local sources, while drpc_pendingTransactions
// (synthetic, no node-backed equivalent) always stays local.
func TestResolveSourceRespectsLocalSubSettings(t *testing.T) {
	newHeads := subscribeRequest(`["newHeads"]`)
	logs := subscribeRequest(`["logs",{}]`)
	pendingTx := subscribeRequest(`["newPendingTransactions"]`)
	drpcPendingTx := subscribeRequest(`["drpc_pendingTransactions"]`)

	resolve := func(req protocol.RequestHolder, settings config.LocalSubSettings) string {
		src, err := resolveSource(chains.ETHEREUM, allCapsSupervisor(), req, nil, nil, nil, settings)
		require.NoError(t, err)
		return src.key
	}

	t.Run("all enabled - every type local", func(t *testing.T) {
		assert.True(t, strings.HasPrefix(resolve(newHeads, allLocalSubs), localNewHeadsPrefix+"|"))
		assert.True(t, strings.HasPrefix(resolve(logs, allLocalSubs), localLogsPrefix+"|"))
		assert.Equal(t, localPendingTxKey, resolve(pendingTx, allLocalSubs))
		assert.Equal(t, localDrpcPendingTxKey, resolve(drpcPendingTx, allLocalSubs))
	})

	t.Run("master off - falls back to generic except drpc", func(t *testing.T) {
		off := config.LocalSubSettings{}
		assert.False(t, strings.HasPrefix(resolve(newHeads, off), localNewHeadsPrefix))
		assert.False(t, strings.HasPrefix(resolve(logs, off), localLogsPrefix))
		assert.NotEqual(t, localPendingTxKey, resolve(pendingTx, off))
		// drpc_pendingTransactions is never gated.
		assert.Equal(t, localDrpcPendingTxKey, resolve(drpcPendingTx, off))
	})

	t.Run("per-type override - only logs stays local", func(t *testing.T) {
		logsOnly := config.LocalSubSettings{Logs: true}
		assert.False(t, strings.HasPrefix(resolve(newHeads, logsOnly), localNewHeadsPrefix))
		assert.True(t, strings.HasPrefix(resolve(logs, logsOnly), localLogsPrefix+"|"))
		assert.NotEqual(t, localPendingTxKey, resolve(pendingTx, logsOnly))
		assert.Equal(t, localDrpcPendingTxKey, resolve(drpcPendingTx, logsOnly))
	})
}

func newHeadsRequest(selectors ...protocol.RequestSelector) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth", selectors...)
}

func TestResolveSourceNewHeadsGate(t *testing.T) {
	resolve := func(sup *mocks.UpstreamSupervisorMock, req protocol.RequestHolder) string {
		src, err := resolveSource(chains.ETHEREUM, sup, req, nil, nil, nil, allLocalSubs)
		require.NoError(t, err)
		return src.key
	}
	local := func(key string) bool { return strings.HasPrefix(key, localNewHeadsPrefix+"|") }

	// websocket head connector -> NewHeadsCap -> local
	assert.True(t, local(resolve(capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap), newHeadsRequest())))

	// a selector some upstream satisfies -> local, and the key carries the selector
	reth := protocol.RequestLabelSelector{Name: "client", Values: []string{"reth"}}
	key := resolve(capsSupervisor(map[string]string{"client": "reth"}, protocol.WsCap, protocol.NewHeadsCap), newHeadsRequest(reth))
	assert.Equal(t, localKey(localNewHeadsPrefix, newHeadsRequest(reth)), key)
	assert.NotEqual(t, localKey(localNewHeadsPrefix, newHeadsRequest()), key, "different selectors -> different sources")
}

// With local newHeads enabled, no upstream passing the full filter right now is
// an error, never a silent fallback onto a node the client did not ask for or
// whose head is polled.
func TestResolveSourceNewHeadsErrorsWhenNoneMatches(t *testing.T) {
	resolveErr := func(sup *mocks.UpstreamSupervisorMock, req protocol.RequestHolder) error {
		_, err := resolveSource(chains.ETHEREUM, sup, req, nil, nil, nil, allLocalSubs)
		return err
	}
	reth := protocol.RequestLabelSelector{Name: "client", Values: []string{"reth"}}

	// a selector no upstream satisfies
	err := resolveErr(capsSupervisor(map[string]string{"client": "geth"}, protocol.WsCap, protocol.NewHeadsCap), newHeadsRequest(reth))
	require.Error(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError().Message, err.(*protocol.ResponseError).Message)

	// the only capable upstream is unavailable
	require.Error(t, resolveErr(unavailableCapsSupervisor(protocol.WsCap, protocol.NewHeadsCap), newHeadsRequest()))

	// an unsupported selector (sort-bearing inside OR) compiles to a matcher no
	// upstream passes
	unsupported := protocol.RequestOrSelector{Children: []protocol.RequestSelector{
		protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest},
		reth,
	}}
	require.Error(t, resolveErr(capsSupervisor(map[string]string{"client": "reth"}, protocol.WsCap, protocol.NewHeadsCap), newHeadsRequest(unsupported)))

	// no upstream advertises NewHeadsCap at all (polled heads): with local
	// newHeads enabled that is an error too, the operator must turn the flag off
	require.Error(t, resolveErr(capsSupervisor(nil, protocol.WsCap), newHeadsRequest()))

	// no chain supervisor at all
	supNil := mocks.NewUpstreamSupervisorMock()
	supNil.On("GetChainSupervisor", chains.ETHEREUM).Return(nil)
	require.Error(t, resolveErr(supNil, newHeadsRequest()))

	// with the flag off the same chain takes the node path, no error
	src, err := resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap), newHeadsRequest(), nil, nil, nil, config.LocalSubSettings{})
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(src.key, localNewHeadsPrefix))
}

func TestSelectorKeyIgnoresAnySelector(t *testing.T) {
	assert.Equal(t, "", selectorKey([]protocol.RequestSelector{protocol.RequestAnySelector{}}))
	label := protocol.RequestLabelSelector{Name: "client", Values: []string{"reth"}}
	assert.Equal(t,
		selectorKey([]protocol.RequestSelector{label}),
		selectorKey([]protocol.RequestSelector{protocol.RequestAnySelector{}, label}))
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

func TestIsPendingTxRequest(t *testing.T) {
	pending := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newPendingTransactions"]`)}, true, "eth")
	drpc := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["drpc_pendingTransactions"]`)}, true, "eth")
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")
	other := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_call", Params: []byte(`[]`)}, false, "eth")

	assert.True(t, isPendingTxRequest(pending))
	assert.False(t, isPendingTxRequest(drpc))
	assert.False(t, isPendingTxRequest(newHeads))
	assert.False(t, isPendingTxRequest(other))

	assert.True(t, isDrpcPendingTxRequest(drpc))
	assert.False(t, isDrpcPendingTxRequest(pending))
	assert.False(t, isDrpcPendingTxRequest(other))
}

func TestLocalPendingTxAvailable(t *testing.T) {
	supNil := mocks.NewUpstreamSupervisorMock()
	supNil.On("GetChainSupervisor", chains.ETHEREUM).Return(nil)
	assert.False(t, localPendingTxAvailable(chains.ETHEREUM, supNil))

	// no ws connector at all → no PendingTxCap → falls back to generic
	supNone := mocks.NewUpstreamSupervisorMock()
	supNone.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap]()},
	})
	assert.False(t, localPendingTxAvailable(chains.ETHEREUM, supNone))

	// a ws connector grants PendingTxCap → local aggregation (no head connector needed)
	supWs := mocks.NewUpstreamSupervisorMock()
	supWs.On("GetChainSupervisor", chains.ETHEREUM).Return(&stubChainSupervisor{
		state: upstreams.ChainSupervisorState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap)},
	})
	assert.True(t, localPendingTxAvailable(chains.ETHEREUM, supWs))
}

func logsRequest(params string, selectors ...protocol.RequestSelector) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(params)}, true, "eth", selectors...)
}

func TestResolveSourceLogsGate(t *testing.T) {
	resolve := func(sup *mocks.UpstreamSupervisorMock, req protocol.RequestHolder) string {
		src, err := resolveSource(chains.ETHEREUM, sup, req, nil, nil, nil, allLocalSubs)
		require.NoError(t, err)
		return src.key
	}
	local := func(key string) bool { return strings.HasPrefix(key, localLogsPrefix+"|") }

	// LogsCap -> local
	assert.True(t, local(resolve(capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap), logsRequest(`["logs",{}]`))))
}

// Sort hints (block tags, predicted lower bounds) order the generic path's
// candidates but do not filter a head feed, so they must not split local sources.
func TestLocalKeyIgnoresSortOnlySelectors(t *testing.T) {
	latest := protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}
	label := protocol.RequestLabelSelector{Name: "client", Values: []string{"reth"}}

	assert.Equal(t, localKey(localLogsPrefix, logsRequest(`["logs",{}]`)), localKey(localLogsPrefix, logsRequest(`["logs",{}]`, latest)))
	assert.Equal(t, localKey(localLogsPrefix, logsRequest(`["logs",{}]`, label)), localKey(localLogsPrefix, logsRequest(`["logs",{}]`, label, latest)))
	assert.NotEqual(t, localKey(localLogsPrefix, logsRequest(`["logs",{}]`)), localKey(localLogsPrefix, logsRequest(`["logs",{}]`, label)))
}

// With local logs on, a malformed filter object is the client's mistake and is
// rejected up front with invalid params, whatever the upstreams look like; it
// is never handed to a node instead.
func TestResolveSourceLogsRejectsMalformedFilter(t *testing.T) {
	bad := logsRequest(`["logs",{"address":123}]`)

	_, err := resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap), bad, nil, nil, nil, allLocalSubs)
	require.Error(t, err)
	assert.Equal(t, protocol.InvalidParams, err.(*protocol.ResponseError).Code)

	// the same on a chain where nothing can serve logs right now
	_, err = resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap), bad, nil, nil, nil, allLocalSubs)
	require.Error(t, err)
	assert.Equal(t, protocol.InvalidParams, err.(*protocol.ResponseError).Code)

	// with the flag off the node decides
	src, err := resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap), bad, nil, nil, nil, config.LocalSubSettings{})
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(src.key, localLogsPrefix))
}

func TestResolveSourceLogsSelectorsSelectTheSource(t *testing.T) {
	reth := protocol.RequestLabelSelector{Name: "client", Values: []string{"reth"}}
	rethSup := func() *mocks.UpstreamSupervisorMock {
		return capsSupervisor(map[string]string{"client": "reth"}, protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap)
	}
	resolve := func(sup *mocks.UpstreamSupervisorMock, req protocol.RequestHolder) string {
		src, err := resolveSource(chains.ETHEREUM, sup, req, nil, nil, nil, allLocalSubs)
		require.NoError(t, err)
		return src.key
	}

	plain := resolve(rethSup(), logsRequest(`["logs",{}]`))
	anySel := resolve(rethSup(), logsRequest(`["logs",{}]`, protocol.RequestAnySelector{}))
	withSel := resolve(rethSup(), logsRequest(`["logs",{}]`, reth))
	assert.True(t, strings.HasPrefix(plain, localLogsPrefix+"|"))
	assert.Equal(t, plain, anySel, "RequestAnySelector is a no-op")
	assert.True(t, strings.HasPrefix(withSel, localLogsPrefix+"|"), "a satisfiable selector stays local")
	assert.NotEqual(t, plain, withSel, "different selectors -> different sources")

	// params do not split the source: per-client filtering happens in the processor
	assert.Equal(t, plain, resolve(rethSup(), logsRequest(`["logs",{"address":"0xabc"}]`)))

}

// Same rule as newHeads: with local logs enabled, nothing matching now is an
// error; only the flag turns the node path back on.
func TestResolveSourceLogsErrorsWhenNoneMatches(t *testing.T) {
	geth := protocol.RequestLabelSelector{Name: "client", Values: []string{"geth"}}
	rethSup := capsSupervisor(map[string]string{"client": "reth"}, protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap)
	_, err := resolveSource(chains.ETHEREUM, rethSup, logsRequest(`["logs",{}]`, geth), nil, nil, nil, allLocalSubs)
	require.Error(t, err)

	_, err = resolveSource(chains.ETHEREUM, unavailableCapsSupervisor(protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap), logsRequest(`["logs",{}]`), nil, nil, nil, allLocalSubs)
	require.Error(t, err)

	// ws head without eth_getLogs: NewHeadsCap but no LogsCap anywhere
	_, err = resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap), logsRequest(`["logs",{}]`), nil, nil, nil, allLocalSubs)
	require.Error(t, err)

	supNil := mocks.NewUpstreamSupervisorMock()
	supNil.On("GetChainSupervisor", chains.ETHEREUM).Return(nil)
	_, err = resolveSource(chains.ETHEREUM, supNil, logsRequest(`["logs",{}]`), nil, nil, nil, allLocalSubs)
	require.Error(t, err)

	src, err := resolveSource(chains.ETHEREUM, capsSupervisor(nil, protocol.WsCap, protocol.NewHeadsCap), logsRequest(`["logs",{}]`, geth), nil, nil, nil, config.LocalSubSettings{})
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(src.key, localLogsPrefix))
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

func grpcStreamRequest(t *testing.T, method string) protocol.RequestHolder {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	return protocol.NewUpstreamGrpcRequest("1", method, nil, []byte{1, 2}, "sui")
}

// gRPC streams are pass-through: identical requests must never share a source.
func TestResolveSourceGrpcStreamKeyIsUniquePerRequest(t *testing.T) {
	request := grpcStreamRequest(t, "/sui.rpc.v2.LedgerService/ListCheckpoints")
	supervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()

	src1, err := resolveSource(chains.SUI, supervisor, request, strategy, nil, nil, config.LocalSubSettings{})
	require.NoError(t, err)
	src2, err := resolveSource(chains.SUI, supervisor, request, strategy, nil, nil, config.LocalSubSettings{})
	require.NoError(t, err)
	key1, key2 := src1.key, src2.key

	prefix := subscriptionKey(request) + "|"
	assert.True(t, strings.HasPrefix(key1, prefix))
	assert.True(t, strings.HasPrefix(key2, prefix))
	assert.NotEqual(t, key1, key2)
}

func TestIsFiniteGrpcStream(t *testing.T) {
	assert.True(t, isFiniteGrpcStream(grpcStreamRequest(t, "/sui.rpc.v2.LedgerService/ListCheckpoints")))
	assert.False(t, isFiniteGrpcStream(grpcStreamRequest(t, "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints")))
	assert.False(t, isFiniteGrpcStream(grpcStreamRequest(t, "/sui.rpc.v2.LedgerService/GetObject")))
	assert.True(t, isGrpcStream(grpcStreamRequest(t, "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints")))
	assert.False(t, isGrpcStream(grpcStreamRequest(t, "/sui.rpc.v2.LedgerService/GetObject")))
}

// blockSubscribe events are whole Solana blocks (megabytes each), so its
// source buffer must be far smaller than the generic one: a subscriber that
// lags by thousands of blocks pins gigabytes and never catches up anyway.
func TestGenericSourceBufferSize_BlockSubscribeIsSmall(t *testing.T) {
	blockSub := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "blockSubscribe", Params: []byte(`["all",{"encoding":"json","transactionDetails":"full"}]`)}, true, "solana")
	newHeads := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth")

	assert.Equal(t, blockSubscribeBufferSize, genericSourceBufferSize(blockSub))
	assert.Equal(t, genericSubscriptionBufferSize, genericSourceBufferSize(newHeads))
	assert.Less(t, blockSubscribeBufferSize, genericSubscriptionBufferSize)
}
