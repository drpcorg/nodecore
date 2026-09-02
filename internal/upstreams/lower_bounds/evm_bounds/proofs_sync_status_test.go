package evm_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func expectProofsSyncStatus(connector *mocks.ConnectorMock, response protocol.ResponseHolder) *mock.Call {
	return connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("debug_proofsSyncStatus"))).
		Return(response)
}

func proofDetectorWithSyncStatus(connector *mocks.ConnectorMock) (*evm_bounds.EvmLowerBoundDetector, *evm_bounds.EvmProofsSyncStatus) {
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	syncStatus := evm_bounds.NewEvmProofsSyncStatus("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).
		WithCapabilities(capabilities).
		WithProofsSyncStatus(syncStatus)
	return detector, syncStatus
}

func boundsByType(bounds []protocol.LowerBoundData) map[protocol.LowerBoundType]int64 {
	result := make(map[protocol.LowerBoundType]int64, len(bounds))
	for _, b := range bounds {
		result[b.Type] = b.Bound
	}
	return result
}

func TestProofsSyncStatusYieldsLowerAndUpperProofBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	detector, _ := proofDetectorWithSyncStatus(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(result))
	assert.Len(t, connector.Calls, 1, "no eth_capabilities, no eth_getProof search")
	assert.Equal(t, []protocol.LowerBoundType{protocol.ProofBound}, detector.SupportedTypes())
}

func TestProofsSyncStatusAcceptsDecimalNumbersAndCoercesEarliestZero(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":0,"latest":200}`)).Once()
	detector, _ := proofDetectorWithSyncStatus(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 1, protocol.UpperProofBound: 200}, boundsByType(result))
}

func TestProofsSyncStatusUnsupportedFallsBackWithoutReprobe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respErr *protocol.ResponseError
	}{
		{"json-rpc code -32601", protocol.NotSupportedMethodError("debug_proofsSyncStatus")},
		{"textual method not found", protocol.ResponseErrorWithMessage("Method not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(tc.respErr)).Once()
			// capabilities answer the proof bound so the search never runs
			expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
			detector, _ := proofDetectorWithSyncStatus(connector)

			first, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

			second, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(second))
			assert.Equal(t, 1, countRequests(connector, "debug_proofsSyncStatus"))
		})
	}
}

func TestProofsSyncStatusMalformedResponseIsUnsupported(t *testing.T) {
	for _, body := range []string{`"garbage"`, `null`, `{"earliest":"0x64"}`, `{"earliest":"latest","latest":"0xc8"}`} {
		t.Run(body, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, evmOK(body)).Once()
			expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
			detector, _ := proofDetectorWithSyncStatus(connector)

			for range 2 {
				result, err := detector.DetectLowerBound(context.Background())
				require.NoError(t, err)
				assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(result))
			}
			assert.Equal(t, 1, countRequests(connector, "debug_proofsSyncStatus"))
		})
	}
}

// A window that is not (yet) usable is not a verdict: an initialising store answers
// latest 0 or earliest > latest and must be asked again next cycle.
func TestProofsSyncStatusEmptyWindowFallsBackAndRetries(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x0","latest":"0x0"}`)).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, _ := proofDetectorWithSyncStatus(connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
}

func TestProofsSyncStatusTransientErrorFallsBackAndRetries(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, _ := proofDetectorWithSyncStatus(connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
	assert.Equal(t, 2, countRequests(connector, "debug_proofsSyncStatus"))
}

func TestProofsSyncStatusReprobesAfterInterval(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, syncStatus := proofDetectorWithSyncStatus(connector)
	syncStatus.SetReprobeInterval(0)

	_, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
}

func TestProofDetectorWithoutSyncStatusNeverAsks(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Once()
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(result))
	assert.Equal(t, 0, countRequests(connector, "debug_proofsSyncStatus"))
}
