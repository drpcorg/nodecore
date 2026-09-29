package flow

import (
	"context"
	"errors"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func pin(ids ...string) protocol.RequestLabelSelector {
	return protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: ids}
}

func pinTestRequest(method string, selectors ...protocol.RequestSelector) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: method}, false, "eth", selectors...)
}

func pinTestMethods(method string) *mocks.MethodsMock {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet(method))
	methodsMock.On("HasMethod", method).Return(true)
	methodsMock.On("HasMethod", mock.Anything).Return(false)
	return methodsMock
}

func publishPinTestUpstream(chainSupervisor upstreams.ChainSupervisor, id string, status protocol.AvailabilityStatus, height uint64, method string) {
	head := protocol.NewBlockWithHeight(height)
	chainSupervisor.PublishUpstreamEvent(test_utils.CreateEvent(id, status, head, pinTestMethods(method)))
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{Id: id, EventType: &protocol.HeadUpstreamEvent{Status: status, Head: head}})
}

func pinTestChain(t *testing.T, method string, ids ...string) upstreams.ChainSupervisor {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	chainSupervisor := upstreams.NewGenericChainSupervisor(t.Context(), chains.ETHEREUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()
	for _, id := range ids {
		publishPinTestUpstream(chainSupervisor, id, protocol.Available, 100, method)
	}
	require.Eventually(t, func() bool { return len(chainSupervisor.GetUpstreamIds()) == len(ids) }, time.Second, time.Millisecond)
	return chainSupervisor
}

func ratingStrategyOf(chainSupervisor upstreams.ChainSupervisor, rated ...string) *RatingStrategy {
	return &RatingStrategy{chainSupervisor: chainSupervisor, ups: rated, selectedUpstreams: mapset.NewThreadUnsafeSet[string]()}
}

// nodeLevel is err as a pinned request gets it
func nodeLevel(err *protocol.ResponseError) *protocol.ResponseError {
	err.NodeLevel = true
	return err
}

func selectAll(t *testing.T, strategy UpstreamStrategy, request protocol.RequestHolder) []string {
	t.Helper()
	selected := make([]string, 0)
	for {
		id, err := strategy.SelectUpstream(request)
		if err != nil {
			expected := protocol.NoAvailableUpstreamsError()
			expected.NodeLevel = request.UpstreamPins().Pinned()
			assert.Equal(t, expected, err)
			return selected
		}
		selected = append(selected, id)
	}
}

func TestUpstreamPinsAreAGate(t *testing.T) {
	geth := protocol.RequestLabelSelector{Name: "client_type", Values: []string{"geth"}}
	or := protocol.RequestOrSelector{Children: []protocol.RequestSelector{pin("x")}}
	selectors := []protocol.RequestSelector{
		pin("a", "b"),
		protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("b", "c"), geth}},
		or,
		// no pin: an ordinary label
		pin(),
	}

	pins, rest := protocol.SplitUpstreamPins(selectors)
	assert.Equal(t, protocol.UpstreamPins{{"a", "b"}, {"b", "c"}}, pins)
	assert.True(t, pins.Admits("b"))
	assert.False(t, pins.Admits("a"))
	assert.False(t, pins.Admits("c"))
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestAndSelector{Children: []protocol.RequestSelector{geth}}, or, pin()}, rest)
	assert.Equal(t, pins, pinTestRequest("eth_call", selectors...).UpstreamPins())

	matchers, order := buildSelectorRouting([]protocol.RequestSelector{pin("a")}, nil, nil)
	assert.Empty(t, matchers)
	assert.Nil(t, order)

	// elsewhere upstream_id is a label no upstream has
	state := protocol.DefaultUpstreamState(nil, nil, "", nil, nil)
	for _, selector := range []protocol.RequestSelector{or, pin()} {
		matchers, _ = buildSelectorRouting([]protocol.RequestSelector{selector}, nil, nil)
		require.Len(t, matchers, 1)
		assert.NotEqual(t, SuccessType, matchers[0].Match("x", &state).Type())
	}
}

func TestEmptyUpstreamIdSelectorFailsLikeAnyLabel(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a")
	selectErr := func(selector protocol.RequestSelector) error {
		matchers, _ := buildSelectorRouting([]protocol.RequestSelector{selector}, nil, chainSupervisor)
		strategy := NewGenericStrategyWithOptions(chainSupervisor, matchers, nil)
		_, err := strategy.SelectUpstream(pinTestRequest("eth_call", selector))
		return err
	}

	err := selectErr(pin())
	assert.Equal(t, protocol.NoAvailableUpstreamsErrorWithCause("a - No label `upstream_id` with values []"), err)
}

func TestPinnedRequestSelectsOnlyPinnedUpstreams(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b", "c")
	request := pinTestRequest("eth_call", protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("b", "c")}})

	strategies := map[string]func() UpstreamStrategy{
		"generic": func() UpstreamStrategy { return NewGenericStrategy(chainSupervisor) },
		"rating":  func() UpstreamStrategy { return ratingStrategyOf(chainSupervisor, "a", "b", "c") },
		"specific order": func() UpstreamStrategy {
			return NewSpecificOrderUpstreamStrategy([]string{"a", "b", "c"}, chainSupervisor)
		},
		"label groups": func() UpstreamStrategy {
			return NewLabelGroupStrategyWithGroups([][]string{{"a"}, {"c"}, {"b"}}, false, chainSupervisor)
		},
	}
	for name, strategy := range strategies {
		t.Run(name, func(t *testing.T) {
			assert.ElementsMatch(t, []string{"b", "c"}, selectAll(t, strategy(), request))
			// unpinned: every upstream, as before
			assert.ElementsMatch(t, []string{"a", "b", "c"}, selectAll(t, strategy(), pinTestRequest("eth_call")))
		})
	}
}

func TestPinnedRequestReachesAnUpstreamTheRatingHasNotListedYet(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	// b joined after the last rating calculation
	rated := []string{"a"}

	assert.Equal(t, []string{"b"}, selectAll(t, ratingStrategyOf(chainSupervisor, rated...), pinTestRequest("eth_call", pin("b"))))
	assert.Equal(t, []string{"b"}, selectAll(t, ratingStrategyOf(chainSupervisor), pinTestRequest("eth_call", pin("b"))))
	assert.Equal(t, []string{"a", "b"}, withUnrated(pinTestRequest("eth_call", pin("b", "a", "gone")), rated, chainSupervisor))
	assert.Equal(t, []string{"a"}, rated)

	// unpinned requests keep the rating list
	assert.Equal(t, []string{"a"}, selectAll(t, ratingStrategyOf(chainSupervisor, rated...), pinTestRequest("eth_call")))
}

func TestPinnedUpstreamsNotPresent(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a")
	// each id once in the message
	request := pinTestRequest("eth_call", pin("x", "y"), protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("y", "x")}})
	expected := nodeLevel(protocol.PinnedUpstreamsNotPresentError([]string{"x", "y"}))
	assert.Equal(t, "pinned upstreams not present: x, y", expected.Message)
	assert.Equal(t, protocol.NoAvailableUpstreams, expected.Code)

	strategies := map[string]UpstreamStrategy{
		"generic":        NewGenericStrategy(chainSupervisor),
		"rating":         ratingStrategyOf(chainSupervisor, "a"),
		"empty rating":   ratingStrategyOf(chainSupervisor),
		"specific order": NewSpecificOrderUpstreamStrategy([]string{"a"}, chainSupervisor),
		"label groups":   NewLabelGroupStrategyWithGroups([][]string{{"a"}, {}}, false, chainSupervisor),
		"no groups":      NewLabelGroupStrategyWithGroups(nil, false, chainSupervisor),
		"empty chain":    NewGenericStrategy(pinTestChain(t, "eth_call")),
	}
	for name, strategy := range strategies {
		t.Run(name, func(t *testing.T) {
			_, err := strategy.SelectUpstream(request)
			assert.Equal(t, expected, err)
		})
	}
}

func TestPinnedUpstreamPresentButUnusable(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a")
	publishPinTestUpstream(chainSupervisor, "down", protocol.Unavailable, 100, "eth_call")
	require.Eventually(t, func() bool { return chainSupervisor.GetUpstreamState("down") != nil }, time.Second, time.Millisecond)

	_, err := NewGenericStrategy(chainSupervisor).SelectUpstream(pinTestRequest("eth_call", pin("down")))
	assert.Equal(t, nodeLevel(protocol.NoAvailableUpstreamsError()), err)

	_, err = NewGenericStrategy(chainSupervisor).SelectUpstream(pinTestRequest("eth_getLogs", pin("a")))
	assert.Equal(t, nodeLevel(protocol.NotSupportedMethodError("eth_getLogs")), err)

	// the cause of a group with the pinned upstream beats the absence in the others
	strategy := NewLabelGroupStrategyWithGroups([][]string{{"down"}, {"a"}}, false, chainSupervisor)
	_, err = strategy.SelectUpstream(pinTestRequest("eth_call", pin("down")))
	assert.Equal(t, nodeLevel(protocol.NoAvailableUpstreamsError()), err)

	// unpinned, the same failures are the request's
	_, err = NewGenericStrategy(chainSupervisor).SelectUpstream(pinTestRequest("eth_getLogs"))
	assert.Equal(t, protocol.NotSupportedMethodError("eth_getLogs"), err)
	_, err = NewSpecificOrderUpstreamStrategy([]string{"down"}, chainSupervisor).SelectUpstream(pinTestRequest("eth_call"))
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

// pinned upstreams that are here but no candidates of the strategy (outside
// every label group, filtered out) fail like the unpinned request would
func TestPinnedUpstreamPresentButNoCandidate(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	request := pinTestRequest("eth_call", pin("b"))

	strategies := map[string]UpstreamStrategy{
		"label groups without default": NewLabelGroupStrategyWithGroups([][]string{{"a"}}, false, chainSupervisor),
		"no groups":                    NewLabelGroupStrategyWithGroups(nil, false, chainSupervisor),
		"specific order":               NewSpecificOrderUpstreamStrategy([]string{"a"}, chainSupervisor),
		"empty specific order":         NewSpecificOrderUpstreamStrategy(nil, chainSupervisor),
	}
	for name, strategy := range strategies {
		t.Run(name, func(t *testing.T) {
			_, err := strategy.SelectUpstream(request)
			assert.Equal(t, nodeLevel(protocol.NoAvailableUpstreamsError()), err)
		})
	}
}

func TestPinnedQuorumWithoutAPinnedSigner(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "drpc-1")
	drpcIds := []string{"drpc-1"}

	assert.Nil(t, quorumPinError(pinTestRequest("eth_call", pin("drpc-1", "a")), drpcIds, chainSupervisor))
	assert.Equal(t,
		nodeLevel(protocol.QuorumNotSupportedError("no pinned DRPC upstream with an HTTP connector")),
		quorumPinError(pinTestRequest("eth_call", pin("a")), drpcIds, chainSupervisor),
	)
	assert.Equal(t,
		nodeLevel(protocol.PinnedUpstreamsNotPresentError([]string{"x"})),
		quorumPinError(pinTestRequest("eth_call", pin("x")), drpcIds, chainSupervisor),
	)
	assert.Nil(t, quorumPinError(pinTestRequest("eth_call"), drpcIds, chainSupervisor))
}

func TestPinnedRoundRobinSharesEvenly(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b", "c", "d")
	request := pinTestRequest("eth_call", pin("a", "d"))

	counts := make(map[string]int)
	for range 1000 {
		id, err := NewGenericStrategy(chainSupervisor).SelectUpstream(request)
		require.NoError(t, err)
		counts[id]++
	}
	assert.Equal(t, map[string]int{"a": 500, "d": 500}, counts)
}

// the pins are parsed once per request: selecting for an unpinned request
// with selectors costs what it costs without them
func TestUnpinnedSelectionAllocations(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b", "c")
	selectors := []protocol.RequestSelector{protocol.RequestAndSelector{Children: []protocol.RequestSelector{
		protocol.RequestLowerHeightSelector{Height: 10},
		protocol.RequestHeightSelector{Height: 100},
	}}}
	allocs := func(request protocol.RequestHolder) float64 {
		return testing.AllocsPerRun(100, func() {
			_, _ = ratingStrategyOf(chainSupervisor, "a", "b", "c").SelectUpstream(request)
		})
	}

	assert.Equal(t, allocs(pinTestRequest("eth_call")), allocs(pinTestRequest("eth_call", selectors...)))
}

type pinTestUpstreams struct {
	supervisor *mocks.UpstreamSupervisorMock
	connectors map[string]*mocks.ConnectorMock
}

func newPinTestUpstreams(chainSupervisor upstreams.ChainSupervisor, ids ...string) *pinTestUpstreams {
	u := &pinTestUpstreams{supervisor: mocks.NewUpstreamSupervisorMock(), connectors: make(map[string]*mocks.ConnectorMock)}
	u.supervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(chainSupervisor).Maybe()
	u.supervisor.On("GetExecutor").Return(resilience.CreateFlowExecutor(resilience.CreateFlowRetryPolicy(&config.RetryConfig{Attempts: 3}))).Maybe()
	for _, id := range ids {
		connector := mocks.NewConnectorMock()
		upstream := test_utils.TestEvmUpstream(connector, &config.Upstream{Id: id, Options: &chains.Options{InternalTimeout: 5 * time.Second}}, mocks.NewMethodsMock(), nil)
		u.supervisor.On("GetUpstream", id).Return(upstream).Maybe()
		u.connectors[id] = connector
	}
	return u
}

func (u *pinTestUpstreams) answer(id string, request protocol.RequestHolder, result string) {
	u.connectors[id].On("SendRequest", mock.Anything, request).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(result), protocol.JsonRpc))
}

func (u *pinTestUpstreams) assertNotSent(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		u.connectors[id].AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
	}
}

func TestPinnedFailuresReachTheReplyAsNodeLevel(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b")
	processor := NewUnaryRequestProcessor(chains.ETHEREUM, ups.supervisor)
	process := func(request protocol.RequestHolder) protocol.ResponseHolder {
		return processor.ProcessRequest(context.Background(), NewGenericStrategy(chainSupervisor), request).(*UnaryResponse).ResponseWrapper.Response
	}

	response := process(pinTestRequest("eth_call", pin("x")))
	assert.Equal(t, nodeLevel(protocol.PinnedUpstreamsNotPresentError([]string{"x"})), response.GetError())
	assert.True(t, protocol.IsNodeLevelError(response))

	// the pinned upstream fails and no other one may retry it
	request := pinTestRequest("eth_call", pin("a"))
	ups.connectors["a"].On("SendRequest", mock.Anything, request).
		Return(protocol.NewPartialFailure(request, protocol.ServerErrorWithCause(errors.New("upstream a request failed"))))
	response = process(request)
	assert.Equal(t, "internal server error: upstream a request failed", response.GetError().Message)
	assert.True(t, protocol.IsNodeLevelError(response))
	ups.assertNotSent(t, "b")
}

func TestPinnedBroadcastStaysInsideThePin(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_sendRawTransaction", "a", "b", "c")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b", "c")
	request := pinTestRequest("eth_sendRawTransaction", pin("a", "c"))
	ups.answer("a", request, `"0xtx"`)
	ups.answer("c", request, `"0xtx"`)

	processor := NewFanoutRequestProcessor(ups.supervisor, specs.DispatchBroadcast)
	response := processor.ProcessRequest(context.Background(), NewGenericStrategy(chainSupervisor), request).(*UnaryResponse).ResponseWrapper

	assert.False(t, response.Response.HasError())
	ups.connectors["a"].AssertNumberOfCalls(t, "SendRequest", 1)
	ups.connectors["c"].AssertNumberOfCalls(t, "SendRequest", 1)
	ups.assertNotSent(t, "b")
}

func TestPinnedNotNullStaysInsideThePin(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_getTransactionByHash", "a", "b", "c")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b", "c")
	request := pinTestRequest("eth_getTransactionByHash", pin("b"))
	ups.answer("b", request, `null`)

	processor := NewNotNullRequestProcessor(ups.supervisor)
	response := processor.ProcessRequest(context.Background(), NewGenericStrategy(chainSupervisor), request).(*UnaryResponse).ResponseWrapper

	assert.Equal(t, []byte(`null`), response.Response.ResponseResult())
	ups.assertNotSent(t, "a", "c")
}

func TestPinnedIntegrityRerouteStaysInsideThePin(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chainSupervisor := upstreams.NewGenericChainSupervisor(t.Context(), chains.ETHEREUM, fork_choice.NewHeightForkChoice(), nil, false, nil)
	go chainSupervisor.Start()
	publishPinTestUpstream(chainSupervisor, "low", protocol.Available, 5, specs.EthBlockNumber)
	publishPinTestUpstream(chainSupervisor, "mid", protocol.Available, 108, specs.EthBlockNumber)
	publishPinTestUpstream(chainSupervisor, "high", protocol.Available, 150, specs.EthBlockNumber)
	require.Eventually(t, func() bool { return chainSupervisor.GetChainState().HeadData.Head.Height == 150 }, time.Second, time.Millisecond)

	ups := newPinTestUpstreams(chainSupervisor, "low", "mid", "high")
	request := pinTestRequest(specs.EthBlockNumber, pin("low", "mid"))
	ups.answer("low", request, `"0x5"`)
	ups.answer("mid", request, `"0x6c"`)

	// high has the best head, but it is outside the pin
	processor := NewIntegrityRequestProcessor(chains.ETHEREUM, ups.supervisor, nil)
	strategy := NewSpecificOrderUpstreamStrategy([]string{"low", "mid", "high"}, chainSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request).(*UnaryResponse).ResponseWrapper

	assert.Equal(t, []byte(`"0x6c"`), response.Response.ResponseResult())
	ups.assertNotSent(t, "high")
}
