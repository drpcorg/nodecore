package evm_bounds_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
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

func evmCapabilitiesDetectors(connector *mocks.ConnectorMock) []*evm_bounds.EvmLowerBoundDetector {
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	return []*evm_bounds.EvmLowerBoundDetector{
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
	// the single eth_capabilities call is the only upstream request: no probes, no searches
	assert.Len(t, connector.Calls, 1)
	connector.AssertExpectations(t)
}

// The production shape: every detector runs on its own goroutine, all sharing one cache.
func TestEvmCapabilitiesConcurrentDetectorsShareOneFetch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()

	detectors := evmCapabilitiesDetectors(connector)

	var wg sync.WaitGroup
	for _, detector := range detectors {
		wg.Add(1)
		go func(d *evm_bounds.EvmLowerBoundDetector) {
			defer wg.Done()
			result, err := d.DetectLowerBound(context.Background())
			assert.NoError(t, err)
			assert.NotEmpty(t, result)
		}(detector)
	}
	wg.Wait()

	assert.Len(t, connector.Calls, 1)
	connector.AssertExpectations(t)
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

func TestEvmCapabilitiesTransientErrorIsNotCachedAsUnsupported(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).Once()
	expectCapabilities(connector, evmOK(evmCapabilitiesFixture)).Once()
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
	require.Len(t, second, 2)
	assert.Equal(t, int64(1), second[0].Bound)

	assert.Equal(t, 2, countRequests(connector, "eth_capabilities"))
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
