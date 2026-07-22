package stellar_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/stellar_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func horizonRootResponse(elderLedger uint64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"horizon_version":"23.0.0","network_passphrase":"Public Global Stellar Network ; September 2015","history_latest_ledger":58387248,"history_elder_ledger":%d,"core_latest_ledger":58387248}`,
		elderLedger,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func horizonRootResponseWithoutElder() protocol.ResponseHolder {
	body := `{"horizon_version":"23.0.0","history_latest_ledger":58387248}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func matchHorizonRootRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "GET#/" && req.RequestType() == protocol.Rest
	}
}

func TestStellarHorizonLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.BlockBound,
			protocol.TxBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 2*time.Minute, detector.Period())
}

func TestStellarHorizonLowerBoundDetector_AllowsBoundDecrease(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	var decreasing lower_bounds.DecreasingBoundDetector = detector
	assert.True(t, decreasing.AllowsBoundDecrease())
}

func TestStellarHorizonLowerBoundDetector_ElderLedgerReturnsBlockAndTxBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(2))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(2), got[protocol.BlockBound])
	assert.Equal(t, int64(2), got[protocol.TxBound])
}

func TestStellarHorizonLowerBoundDetector_RetriesTransientErrorThenSucceeds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(42))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(42), got[protocol.BlockBound])
}

func TestStellarHorizonLowerBoundDetector_ErrorReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestStellarHorizonLowerBoundDetector_ZeroElderLedgerReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(0))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.ErrorContains(t, err, "no history_elder_ledger")
	assert.Nil(t, result)
}

func TestStellarHorizonLowerBoundDetector_AbsentElderLedgerReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponseWithoutElder())

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.ErrorContains(t, err, "no history_elder_ledger")
	assert.Nil(t, result)
}
