package flow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	specs "github.com/drpcorg/public/pkg/methods"
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
	methodsMock.On("GetMethod", mock.Anything).Return(nil)
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
	chainSupervisor := upstreams.NewGenericChainSupervisor(t.Context(), chains.ETHEREUM, fork_choice.NewHeightForkChoice, nil, false, nil)
	go chainSupervisor.Start()
	for _, id := range ids {
		publishPinTestUpstream(chainSupervisor, id, protocol.Available, 100, method)
	}
	require.Eventually(t, func() bool { return len(chainSupervisor.GetUpstreamIds()) == len(ids) }, time.Second, time.Millisecond)
	return chainSupervisor
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

func (u *pinTestUpstreams) flow(cfg *config.UpstreamConfig) *GenericExecutionFlow {
	return &GenericExecutionFlow{chain: chains.ETHEREUM, upstreamSupervisor: u.supervisor, appConfig: &config.AppConfig{UpstreamConfig: cfg}}
}

func TestPinnedAttemptHasOneCandidateWithoutRating(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b")
	for _, balancing := range []config.BalancingStrategy{config.BaseBalancingStrategy, config.RatingBalancingStrategy} {
		exec := ups.flow(&config.UpstreamConfig{BalancingStrategy: balancing})
		request := pinTestRequest("eth_call", pin("b"))
		strategy := exec.createStrategy(t.Context(), request)
		id, err := strategy.SelectUpstream(request)
		require.NoError(t, err)
		require.Equal(t, "b", id)
		_, err = strategy.SelectUpstream(request)
		require.Error(t, err, "retry or hedge cannot select a second node")
		require.True(t, err.(*protocol.ResponseError).NodeLevel)
	}
}

func TestPinnedHedgesSelectTheNodeOnce(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	request := pinTestRequest("eth_call", pin("a"))
	matchers := make([]Matcher, 1, 8)
	matchers[0] = upstreamIDMatcher{id: "a"}
	strategy := newPinnedStrategy("a", chainSupervisor, matchers)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			id, _ := strategy.SelectUpstream(request)
			results <- id
		})
	}
	wg.Wait()
	close(results)
	selected := 0
	for id := range results {
		if id != "" {
			require.Equal(t, "a", id)
			selected++
		}
	}
	require.Equal(t, 1, selected)
}

func TestPinnedSelectionFailures(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a")
	publishPinTestUpstream(chainSupervisor, "down", protocol.Unavailable, 100, "eth_call")
	require.Eventually(t, func() bool { return chainSupervisor.GetUpstreamState("down") != nil }, time.Second, time.Millisecond)
	exec := newPinTestUpstreams(chainSupervisor, "a", "down").flow(&config.UpstreamConfig{})
	for _, tc := range []struct {
		id, method string
		code       int
	}{
		{"missing", "eth_call", protocol.NoAvailableUpstreams},
		{"down", "eth_call", protocol.NoAvailableUpstreams},
		{"a", "eth_getLogs", protocol.NoSupportedMethod},
	} {
		request := pinTestRequest(tc.method, pin(tc.id))
		_, err := exec.createStrategy(t.Context(), request).SelectUpstream(request)
		require.Error(t, err)
		require.Equal(t, tc.code, err.(*protocol.ResponseError).Code)
		require.True(t, err.(*protocol.ResponseError).NodeLevel)
	}
	request := pinTestRequest("eth_call", pin("a", "down"))
	_, err := exec.createStrategy(t.Context(), request).SelectUpstream(request)
	require.Equal(t, protocol.InvalidParamsError("upstream_id must select exactly one nonempty ID at the top level or under AND"), err)
}

func TestPinnedAttemptKeepsAuthorizationAndLabelExclusions(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b")
	request := pinTestRequest("eth_call", pin("a"), protocol.RequestGroupLabelSelector{Labels: []string{"private"}})
	exec := ups.flow(&config.UpstreamConfig{})
	_, err := exec.createStrategy(t.Context(), request).SelectUpstream(request)
	require.Error(t, err, "an upstream pin must not bypass authorization")
	cfg := &config.UpstreamConfig{LabelBalancing: &config.LabelBalancingConfig{Order: []string{"private"}, IncludeDefault: new(false)}}
	request = pinTestRequest("eth_call", pin("a"))
	_, err = ups.flow(cfg).createStrategy(t.Context(), request).SelectUpstream(request)
	require.Error(t, err, "an excluded upstream must stay excluded")
	cfg.LabelBalancing.IncludeDefault = new(true)
	id, err := ups.flow(cfg).createStrategy(t.Context(), request).SelectUpstream(request)
	require.NoError(t, err)
	require.Equal(t, "a", id)
}

func TestPinnedQuorumRequiresSelectedSigner(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a")
	exec := newPinTestUpstreams(chainSupervisor, "a").flow(&config.UpstreamConfig{})
	ctx := quorum.WithParams(t.Context(), quorum.Params{Quorum: 1, QuorumOf: 1})
	request := pinTestRequest("eth_call", pin("a"))
	_, err := exec.createStrategy(ctx, request).SelectUpstream(request)
	require.Error(t, err)
	require.True(t, err.(*protocol.ResponseError).NodeLevel)
	require.Equal(t, protocol.QuorumNotSupportedError("selected upstream cannot serve a quorum request").Code, err.(*protocol.ResponseError).Code)
	request = pinTestRequest("eth_call", pin("missing"))
	_, err = exec.createStrategy(ctx, request).SelectUpstream(request)
	require.Equal(t, protocol.UpstreamNotPresentError("missing"), err)
}

func TestPinnedFailuresReachTheReplyAsNodeLevel(t *testing.T) {
	chainSupervisor := pinTestChain(t, "eth_call", "a", "b")
	ups := newPinTestUpstreams(chainSupervisor, "a", "b")
	exec := ups.flow(&config.UpstreamConfig{})
	processor := NewUnaryRequestProcessor(chains.ETHEREUM, ups.supervisor)
	process := func(request protocol.RequestHolder) protocol.ResponseHolder {
		return processor.ProcessRequest(context.Background(), exec.createStrategy(t.Context(), request), request).(*UnaryResponse).ResponseWrapper.Response
	}
	response := process(pinTestRequest("eth_call", pin("missing")))
	require.Equal(t, protocol.UpstreamNotPresentError("missing"), response.GetError())
	require.True(t, protocol.IsNodeLevelError(response))
	request := pinTestRequest("eth_call", pin("a"))
	ups.connectors["a"].On("SendRequest", mock.Anything, request).
		Return(protocol.NewPartialFailure(request, protocol.ServerErrorWithCause(errors.New("upstream a request failed"))))
	response = process(request)
	require.True(t, protocol.IsNodeLevelError(response))
	ups.assertNotSent(t, "b")
}

func TestPinnedDispatchAndIntegrityCannotUseAnotherNode(t *testing.T) {
	for _, method := range []string{"eth_sendRawTransaction", "eth_getTransactionByHash", specs.EthBlockNumber} {
		t.Run(method, func(t *testing.T) {
			chainSupervisor := pinTestChain(t, method, "a", "b")
			ups := newPinTestUpstreams(chainSupervisor, "a", "b")
			request := pinTestRequest(method, pin("a"))
			result := `"0x1"`
			if method == "eth_getTransactionByHash" {
				result = `null`
			}
			ups.answer("a", request, result)
			exec := ups.flow(&config.UpstreamConfig{})
			var processor RequestProcessor
			switch method {
			case "eth_sendRawTransaction":
				processor = NewFanoutRequestProcessor(ups.supervisor, specs.DispatchBroadcast)
			case "eth_getTransactionByHash":
				processor = NewNotNullRequestProcessor(ups.supervisor)
			default:
				processor = NewIntegrityRequestProcessor(chains.ETHEREUM, ups.supervisor, nil)
			}
			response := processor.ProcessRequest(t.Context(), exec.createStrategy(t.Context(), request), request).(*UnaryResponse).ResponseWrapper
			require.False(t, response.Response.HasError())
			ups.connectors["a"].AssertNumberOfCalls(t, "SendRequest", 1)
			ups.assertNotSent(t, "b")
		})
	}
}
