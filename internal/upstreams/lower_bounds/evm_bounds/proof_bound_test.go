package evm_bounds_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stateproofs.oldestBlock below the reported head: the value is trusted, bound 42.
const capsProofsBelowHead = `{"head":{"number":"0x1000"},"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`

// the op-sepolia shape: oldestBlock == head.number, so the report says nothing usable.
const capsProofsAtHead = `{"head":{"number":"0x1000"},"stateproofs":{"disabled":false,"oldestBlock":"0x1000"}}`

func expectProofsSyncStatus(connector *mocks.ConnectorMock, response protocol.ResponseHolder) *mock.Call {
	return connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("debug_proofsSyncStatus"))).
		Return(response)
}

func boundsByType(bounds []protocol.LowerBoundData) map[protocol.LowerBoundType]int64 {
	result := make(map[protocol.LowerBoundType]int64, len(bounds))
	for _, b := range bounds {
		result[b.Type] = b.Bound
	}
	return result
}

// evmProofHeight extracts the numeric block height from an eth_getProof request.
func evmProofHeight(request protocol.RequestHolder) (int64, bool) {
	if request.Method() != "eth_getProof" {
		return 0, false
	}
	body, err := request.Body()
	if err != nil {
		return 0, false
	}
	node, err := sonic.Get(body, "params", 2)
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

// expectProofsAbove wires eth_getProof to return a proof for heights >= threshold and null
// below it, for any number of probes.
func expectProofsAbove(connector *mocks.ConnectorMock, threshold int64) {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmProofHeight(r)
			return ok && h >= threshold
		})).
		Return(evmOK(`{"accountProof":[]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmProofHeight(r)
			return ok && h < threshold
		})).
		Return(evmOK(`null`)).
		Maybe()
}

func proofDetector(connector *mocks.ConnectorMock) *evm_bounds.EvmProofLowerBoundDetector {
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	return evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).
		WithCapabilities(capabilities)
}

func TestProofDetectorSyncStatusYieldsBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	detector := proofDetector(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100}, boundsByType(result))
	assert.Len(t, connector.Calls, 1, "no eth_capabilities, no eth_getProof search")
	assert.Equal(t, []protocol.LowerBoundType{protocol.ProofBound}, detector.SupportedTypes())
	assert.Equal(t, 3*time.Minute, detector.Period())
	connector.AssertExpectations(t)
}

func TestProofDetectorSyncStatusAcceptsDecimalAndCoercesZero(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":0,"latest":200}`)).Once()
	detector := proofDetector(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 1}, boundsByType(result))
	connector.AssertExpectations(t)
}

// The reviewer requirement: no memoized verdict. An upstream without the method is asked
// again on the next cycle, and each cycle falls through to eth_capabilities.
func TestProofDetectorSyncStatusRejectedFallsToCapabilitiesEveryCycle(t *testing.T) {
	testCases := []struct {
		name    string
		respErr *protocol.ResponseError
	}{
		{"json-rpc code -32601", protocol.NotSupportedMethodError("debug_proofsSyncStatus")},
		{"textual method not found", protocol.ResponseErrorWithMessage("Method not found")},
		{"unclassified error", protocol.ResponseErrorWithMessage("boom")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(tc.respErr)).Times(2)
			expectCapabilities(connector, evmOK(capsProofsBelowHead)).Once()
			detector := proofDetector(connector)

			first, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

			second, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(second))

			assert.Equal(t, 2, countRequests(connector, "debug_proofsSyncStatus"))
			connector.AssertExpectations(t)
		})
	}
}

func TestProofDetectorSyncStatusMalformedOrEmptyFallsToCapabilities(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{"not an object", `"garbage"`},
		{"null result", `null`},
		{"latest missing", `{"earliest":"0x64"}`},
		{"unparseable earliest", `{"earliest":"latest","latest":"0xc8"}`},
		{"empty store", `{"earliest":"0x0","latest":"0x0"}`},
		{"inverted window", `{"earliest":"0xc8","latest":"0x64"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, evmOK(tc.body)).Once()
			expectCapabilities(connector, evmOK(capsProofsBelowHead)).Once()
			detector := proofDetector(connector)

			result, err := detector.DetectLowerBound(context.Background())

			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(result))
			connector.AssertExpectations(t)
		})
	}
}

// The op-sepolia regression: oldestBlock == head.number must never be published as the
// proof bound. The search result stands instead.
func TestProofDetectorCapabilitiesAtHeadFallsToSearch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectCapabilities(connector, evmOK(capsProofsAtHead)).Once()
	expectLatest(connector, "0x5")
	expectProofsAbove(connector, 3)
	detector := proofDetector(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 3}, boundsByType(result))
	assert.Greater(t, countRequests(connector, "eth_getProof"), 0)
	connector.AssertExpectations(t)
}

// A report without a head cannot be validated, so its stateproofs value is unusable.
func TestProofDetectorCapabilitiesWithoutHeadFallsToSearch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Once()
	expectLatest(connector, "0x5")
	expectProofsAbove(connector, 3)
	detector := proofDetector(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 3}, boundsByType(result))
	connector.AssertExpectations(t)
}

func TestProofDetectorCapabilitiesDisabledPublishesNothing(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectCapabilities(connector, evmOK(`{"head":{"number":"0x1000"},"stateproofs":{"disabled":true}}`)).Once()
	detector := proofDetector(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, 0, countRequests(connector, "eth_blockNumber"))
	assert.Equal(t, 0, countRequests(connector, "eth_getProof"))
	connector.AssertExpectations(t)
}

func TestProofDetectorWithoutCapabilitiesSearches(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectLatest(connector, "0x5")
	expectProofsAbove(connector, 3)
	detector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 3}, boundsByType(result))
	assert.Equal(t, 0, countRequests(connector, "eth_capabilities"))
	connector.AssertExpectations(t)
}
