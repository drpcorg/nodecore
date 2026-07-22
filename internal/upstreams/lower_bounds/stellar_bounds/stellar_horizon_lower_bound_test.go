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

func horizonRootResponse(elderLedger uint64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"horizon_version":"27.0.0-a5df6aaa4b2b5e21e5a9d3e77d0344e57197b1d6",`+
			`"core_version":"stellar-core 27.1.0 (bd64dbb0f508f21c8ed3374615d4c9e1a1e5cb9a)",`+
			`"ingest_latest_ledger":63562891,"history_latest_ledger":63562891,`+
			`"history_latest_ledger_closed_at":"2026-07-20T09:58:54Z",`+
			`"history_elder_ledger":%d,"core_latest_ledger":63562891,`+
			`"network_passphrase":"Public Global Stellar Network ; September 2015",`+
			`"current_protocol_version":27}`,
		elderLedger,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func horizonRootResponseWithoutElder() protocol.ResponseHolder {
	body := `{"horizon_version":"27.0.0","history_latest_ledger":63562891,` +
		`"network_passphrase":"Public Global Stellar Network ; September 2015"}`
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
			protocol.UnknownBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 2*time.Minute, detector.Period())
}

func TestStellarHorizonLowerBoundDetector_AllowsBoundDecrease(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	assert.True(t, detector.AllowsBoundDecrease())
}

func TestStellarHorizonLowerBoundDetector_ElderLedgerReturnsBlockAndTxBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(57255121))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(57255121), got[protocol.BlockBound])
	assert.Equal(t, int64(57255121), got[protocol.TxBound])
}

func TestStellarHorizonLowerBoundDetector_DecreasedElderLedgerIsReturnedAsIs(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(57255121)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(40000000))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(57255121), boundsByType(t, result)[protocol.BlockBound])

	// the operator ran `horizon db reingest range` deeper: the lower bound
	// legally decreases and the detector publishes the new value as is
	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(40000000), got[protocol.BlockBound])
	assert.Equal(t, int64(40000000), got[protocol.TxBound])
}

func TestStellarHorizonLowerBoundDetector_ZeroElderLedgerWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(0))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestStellarHorizonLowerBoundDetector_AbsentElderLedgerWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponseWithoutElder())

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestStellarHorizonLowerBoundDetector_FetchErrorRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(horizonRootResponse(57255121)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(57255121), got[protocol.BlockBound])
	assert.Equal(t, int64(57255121), got[protocol.TxBound])
}

func TestStellarHorizonLowerBoundDetector_FetchErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHorizonRootRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := stellar_bounds.NewStellarHorizonLowerBoundDetector("id", chains.STELLAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
