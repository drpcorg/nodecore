package evm_bounds_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// geth-shaped fixture: head and deleteStrategy must be ignored, blocks "0x0" must map to bound 1.
const evmCapabilitiesFixture = `{
  "head": {"number":"0x1000","hash":"0xaa"},
  "state": {"disabled":false,"oldestBlock":"0x64","deleteStrategy":{"blockAmount":"0x7f000","type":"window"}},
  "tx": {"disabled":false,"oldestBlock":"0x32","deleteStrategy":{"type":"archive"}},
  "logs": {"disabled":false,"oldestBlock":"0x28"},
  "receipts": {"disabled":false,"oldestBlock":"0x3c"},
  "blocks": {"disabled":false,"oldestBlock":"0x0"},
  "stateproofs": {"disabled":false,"oldestBlock":"0xc8"}
}`

func countRequests(connector *mocks.ConnectorMock, method string) int {
	count := 0
	for _, call := range connector.Calls {
		if request, ok := call.Arguments.Get(1).(protocol.RequestHolder); ok && request.Method() == method {
			count++
		}
	}
	return count
}

func expectCapabilities(connector *mocks.ConnectorMock, response protocol.ResponseHolder) *mock.Call {
	return connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_capabilities"))).
		Return(response)
}

// evmBalanceHeight extracts the numeric block height from an eth_getBalance request.
func evmBalanceHeight(request protocol.RequestHolder) (int64, bool) {
	if request.Method() != "eth_getBalance" {
		return 0, false
	}
	body, err := request.Body()
	if err != nil {
		return 0, false
	}
	node, err := sonic.Get(body, "params", 1)
	if err != nil {
		return 0, false
	}
	raw, err := node.String()
	if err != nil {
		return 0, false
	}
	h, err := strconv.ParseInt(strings.TrimPrefix(raw, "0x"), 16, 64)
	if err != nil {
		return 0, false
	}
	return h, true
}

// expectStateAbove disables state override and wires eth_getBalance to serve heights >= threshold
// and fail below it with missingState, for any number of probes.
func expectStateAbove(connector *mocks.ConnectorMock, threshold int64, missingState *protocol.ResponseError) {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call"))).
		Return(evmOK(`"0x"`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBalanceHeight(r)
			return ok && h >= threshold
		})).
		Return(evmOK(`"0x0"`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBalanceHeight(r)
			return ok && h < threshold
		})).
		Return(protocol.NewHttpUpstreamResponseWithError(missingState)).
		Maybe()
}

func missingTrieNode() *protocol.ResponseError {
	return protocol.ResponseErrorWithMessage("missing trie node")
}

func evmCapabilitiesDetectors(connector *mocks.ConnectorMock) []lower_bounds.LowerBoundDetector {
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	return []lower_bounds.LowerBoundDetector{
		evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities),
	}
}

func TestEvmCapabilitiesServeAllBoundTypesWithSingleCall(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectStateAbove(connector, 100, missingTrieNode())

	detectors := evmCapabilitiesDetectors(connector)

	bounds := make(map[protocol.LowerBoundType]int64)
	for _, detector := range detectors {
		result, err := detector.DetectLowerBound(context.Background())
		require.NoError(t, err)
		for _, data := range result {
			bounds[data.Type] = data.Bound
		}
	}

	expected := map[protocol.LowerBoundType]int64{
		protocol.StateBound:    100,
		protocol.TraceBound:    100,
		protocol.BlockBound:    1,
		protocol.LogsBound:     40,
		protocol.TxBound:       50,
		protocol.ReceiptsBound: 60,
		protocol.ProofBound:    200,
	}
	assert.Equal(t, expected, bounds)
	// one eth_capabilities call, the proof detector's rejected sync status and the state check
	// below the reported bound (override support + balance): no searches
	assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
	assert.Equal(t, 1, countRequests(connector, "eth_getBalance"))
	assert.Len(t, connector.Calls, 4)
	connector.AssertExpectations(t)
}

// The production shape: every detector runs on its own goroutine, all sharing one cache.
func TestEvmCapabilitiesConcurrentDetectorsShareOneFetch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectStateAbove(connector, 100, missingTrieNode())

	detectors := evmCapabilitiesDetectors(connector)

	var wg sync.WaitGroup
	for _, detector := range detectors {
		wg.Add(1)
		go func(d lower_bounds.LowerBoundDetector) {
			defer wg.Done()
			result, err := d.DetectLowerBound(context.Background())
			assert.NoError(t, err)
			assert.NotEmpty(t, result)
		}(detector)
	}
	wg.Wait()

	assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
	assert.Len(t, connector.Calls, 4)
	connector.AssertExpectations(t)
}

// An upstream serving state below its reported state.oldestBlock gets the state bound
// from the search; the other types keep the reported values.
func TestEvmCapabilitiesStateBelowReportedBoundFallsToSearch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
	expectStateAbove(connector, 40, missingTrieNode())
	expectLatest(connector, "0x1000")

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	stateDetector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)
	blockDetector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	stateResult, err := stateDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.StateBound: 40, protocol.TraceBound: 40}, boundsByType(stateResult))

	blockResult, err := blockDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.BlockBound: 1, protocol.LogsBound: 40}, boundsByType(blockResult))

	assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
	assert.Equal(t, 1, countRequests(connector, "eth_blockNumber"))
	connector.AssertExpectations(t)
}

func TestEvmCapabilitiesStateKeptWhenBelowReportedBoundIsUnavailable(t *testing.T) {
	testCases := []struct {
		name         string
		missingState *protocol.ResponseError
	}{
		{"no-data error", missingTrieNode()},
		{"probe failure", protocol.ResponseErrorWithMessage("boom")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
			expectStateAbove(connector, 100, tc.missingState)

			capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
			detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

			result, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.StateBound: 100, protocol.TraceBound: 100}, boundsByType(result))

			// a single probe at oldestBlock - 1, no search
			assert.Equal(t, 1, countRequests(connector, "eth_getBalance"))
			assert.Equal(t, 0, countRequests(connector, "eth_blockNumber"))
		})
	}
}

func TestEvmCapabilitiesArchiveStateIsNotProbed(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(`{"state":{"disabled":false,"oldestBlock":"0x0"}}`)).Once()

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.StateBound: 1, protocol.TraceBound: 1}, boundsByType(result))
	assert.Len(t, connector.Calls, 1)
}

func TestEvmCapabilitiesZeroOldestBlockYieldsBoundOne(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(`{"blocks":{"disabled":false,"oldestBlock":"0x0"},"logs":{"disabled":false,"oldestBlock":"0x0"}}`)).Once()

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, data := range result {
		assert.Equal(t, int64(1), data.Bound)
	}
}

func TestEvmCapabilitiesDisabledResourceYieldsNoBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	fixture := `{
	  "state": {"disabled":true},
	  "tx": {"disabled":false,"oldestBlock":"0x32"},
	  "logs": {"disabled":false,"oldestBlock":"0x28"},
	  "receipts": {"disabled":false,"oldestBlock":"0x3c"},
	  "blocks": {"disabled":false,"oldestBlock":"0x0"},
	  "stateproofs": {"disabled":false,"oldestBlock":"0xc8"}
	}`
	expectCapabilities(connector, evmOK(fixture)).Once()

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	stateDetector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)
	blockDetector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	stateResult, err := stateDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Empty(t, stateResult)

	blockResult, err := blockDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Len(t, blockResult, 2)

	// the disabled state must not trigger any state probing either
	assert.Len(t, connector.Calls, 1)
}

func TestEvmCapabilitiesUnsupportedMethodFallsBackToSearchWithoutRetry(t *testing.T) {
	testCases := []struct {
		name    string
		respErr *protocol.ResponseError
	}{
		{"json-rpc code -32601", protocol.NotSupportedMethodError("eth_capabilities")},
		{"textual method not found", protocol.ResponseErrorWithMessage("Method not found")},
		{"textual unsupported method", protocol.ResponseErrorWithMessage("unsupported method: eth_capabilities")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectCapabilities(connector, protocol.NewHttpUpstreamResponseWithError(tc.respErr)).Once()
			expectLatest(connector, "0x5")
			expectLatest(connector, "0x5")
			expectBlocksAbove(connector, 3, `{"number":"0x3","transactions":[]}`)

			capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
			// zero result ttl: every cycle would re-ask if the unsupported verdict were not cached
			capabilities.SetProbeWindows(0, time.Hour)
			detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

			first, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, first)
			assert.Equal(t, int64(3), first[0].Bound)

			second, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, second)
			assert.Equal(t, int64(3), second[0].Bound)

			assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
		})
	}
}

func TestEvmCapabilitiesMalformedResponseFallsBackToSearchWithoutRetry(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{"not an object", `"garbage"`},
		{"null result", `null`},
		{"no usable entries", `{"head":{"number":"0x1000"}}`},
		{"unparseable oldest blocks", `{"state":{"disabled":false,"oldestBlock":"latest"},"blocks":{"disabled":false}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectCapabilities(connector, evmOK(tc.body)).Once()
			expectLatest(connector, "0x5")
			expectLatest(connector, "0x5")
			expectBlocksAbove(connector, 3, `{"number":"0x3","transactions":[]}`)

			capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
			capabilities.SetProbeWindows(0, time.Hour)
			detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

			first, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, first)
			assert.Equal(t, int64(3), first[0].Bound)

			second, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, second)
			assert.Equal(t, int64(3), second[0].Bound)

			assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
		})
	}
}

// A single hiccup is retried within the same refresh: the cycle stays on the
// capabilities path and no search probes are issued.
func TestEvmCapabilitiesTransientErrorRetriedWithinRefresh(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).Once()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	capabilities.SetRetryPolicy(3, time.Millisecond)
	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, int64(1), result[0].Bound)
	assert.Len(t, connector.Calls, 2, "retry must be the only extra upstream request")
	connector.AssertExpectations(t)
}

func TestEvmCapabilitiesTransientErrorIsNotCachedAsUnsupported(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).Times(3)
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
	expectLatest(connector, "0x5")
	expectBlocksAbove(connector, 3, `{"number":"0x3","transactions":[]}`)

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	capabilities.SetProbeWindows(0, time.Hour)
	capabilities.SetRetryPolicy(3, time.Millisecond)
	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	// all attempts fail: this cycle falls back to the search
	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, first)
	assert.Equal(t, int64(3), first[0].Bound)

	// next window succeeds: the failure was not cached as unsupported
	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, int64(1), second[0].Bound)

	assert.Equal(t, 4, countRequests(connector, "eth_capabilities"))
}

func TestEvmCapabilitiesReprobeUnsupportedAfterInterval(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("eth_capabilities"))).Once()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
	expectLatest(connector, "0x5")
	expectBlocksAbove(connector, 3, `{"number":"0x3","transactions":[]}`)

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	capabilities.SetProbeWindows(time.Hour, 30*time.Millisecond)
	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, first)
	assert.Equal(t, int64(3), first[0].Bound)

	time.Sleep(50 * time.Millisecond)

	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, int64(1), second[0].Bound)

	assert.Equal(t, 2, countRequests(connector, "eth_capabilities"))
}

// A report that doesn't cover a detector's types sends only that detector to the search;
// the others keep using the same single fetch.
func TestEvmCapabilitiesPartialResponseFallsBackPerDetector(t *testing.T) {
	connector := mocks.NewConnectorMock()
	fixtureWithoutProofs := `{
	  "state": {"disabled":false,"oldestBlock":"0x64"},
	  "tx": {"disabled":false,"oldestBlock":"0x32"},
	  "logs": {"disabled":false,"oldestBlock":"0x28"},
	  "receipts": {"disabled":false,"oldestBlock":"0x3c"},
	  "blocks": {"disabled":false,"oldestBlock":"0x0"}
	}`
	expectCapabilities(connector, evmOK(fixtureWithoutProofs)).Once()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getProof"))).
		Return(evmOK(`{"accountProof":[]}`)).
		Maybe()

	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	txDetector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)
	proofDetector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	txResult, err := txDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, txResult, 1)
	assert.Equal(t, protocol.TxBound, txResult[0].Type)
	assert.Equal(t, int64(50), txResult[0].Bound)

	proofResult, err := proofDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, proofResult, 1)
	assert.Equal(t, protocol.ProofBound, proofResult[0].Type)
	assert.Equal(t, int64(1), proofResult[0].Bound)

	assert.Equal(t, 1, countRequests(connector, "eth_capabilities"))
}
