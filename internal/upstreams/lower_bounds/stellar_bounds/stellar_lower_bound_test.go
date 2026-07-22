package stellar_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/stellar_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func stellarHealthResponse(oldestLedger uint64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":"1","result":{"status":"healthy","latestLedger":63525714,"latestLedgerCloseTime":"1784332881","oldestLedger":%d,"oldestLedgerCloseTime":"1783727881","ledgerRetentionWindow":120960}}`,
		oldestLedger,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func stellarHealthResponseWithoutOldest() protocol.ResponseHolder {
	body := `{"jsonrpc":"2.0","id":"1","result":{"status":"healthy","latestLedger":63525714}}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func matchStellarHealthRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "getHealth" && req.RequestType() == protocol.JsonRpc
	}
}

func boundsByType(t *testing.T, result []protocol.LowerBoundData) map[protocol.LowerBoundType]int64 {
	t.Helper()
	got := make(map[protocol.LowerBoundType]int64, len(result))
	for _, b := range result {
		got[b.Type] = b.Bound
	}
	return got
}

func TestStellarLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.BlockBound,
			protocol.TxBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 2*time.Minute, detector.Period())
}

func TestStellarLowerBoundDetector_OldestLedgerReturnsBlockAndTxBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(stellarHealthResponse(63404754))

	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(63404754), got[protocol.BlockBound])
	assert.Equal(t, int64(63404754), got[protocol.TxBound])
}

func TestStellarLowerBoundDetector_RetriesTransientErrorThenSucceeds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(stellarHealthResponse(63404754))

	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(63404754), got[protocol.BlockBound])
}

func TestStellarLowerBoundDetector_ErrorReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestStellarLowerBoundDetector_ZeroOldestLedgerReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(stellarHealthResponse(0))

	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.ErrorContains(t, err, "no oldestLedger")
	assert.Nil(t, result)
}

func TestStellarLowerBoundDetector_AbsentOldestLedgerReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarHealthRequest())).
		Return(stellarHealthResponseWithoutOldest())

	detector := stellar_bounds.NewStellarLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.ErrorContains(t, err, "no oldestLedger")
	assert.Nil(t, result)
}
