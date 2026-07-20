package ton_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/ton_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func tonV3MasterchainInfoResponse(firstSeqno uint64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"last":{"workchain":-1,"shard":"8000000000000000","seqno":80354724,`+
			`"root_hash":"hoPjlUCYxfZMeIjBrxNit+ToEYTj3rtBhG9ZFIUgspM=",`+
			`"file_hash":"SFEPKoSzgQ2mouMgL0wfqBYXG0HA1xUvX76Mw43ykKY=",`+
			`"gen_utime":"1784334147","global_id":-239},`+
			`"first":{"workchain":-1,"shard":"8000000000000000","seqno":%d,`+
			`"root_hash":"VpWYYOvJx1FBFdGpRjB1WK1IArc8/W9MYbLUBLxOn5I=",`+
			`"file_hash":"pOu2eaWvBw1EGXhFvMKOOnEnLPWB9zSWCF9Nl2xa/ss=",`+
			`"gen_utime":"1780000000","global_id":-239}}`,
		firstSeqno,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func matchTonV3MasterchainInfoRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "GET#/api/v3/masterchainInfo" && req.RequestType() == protocol.Rest
	}
}

func TestTonV3LowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.StateBound,
			protocol.BlockBound,
			protocol.UnknownBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 3*time.Minute, detector.Period())
}

func TestTonV3LowerBoundDetector_AllowsBoundDecrease(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	assert.True(t, detector.AllowsBoundDecrease())
}

func TestTonV3LowerBoundDetector_FirstSeqnoReturnsStateAndBlockBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(tonV3MasterchainInfoResponse(66051993))

	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(66051993), got[protocol.StateBound])
	assert.Equal(t, int64(66051993), got[protocol.BlockBound])
}

func TestTonV3LowerBoundDetector_DecreasedFirstSeqnoIsReturnedAsIs(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(tonV3MasterchainInfoResponse(66051993)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(tonV3MasterchainInfoResponse(50000000))

	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(66051993), boundsByType(t, result)[protocol.BlockBound])

	// the operator re-backfilled the index deeper: the lower bound legally
	// decreases and the detector publishes the new value as is
	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(50000000), got[protocol.StateBound])
	assert.Equal(t, int64(50000000), got[protocol.BlockBound])
}

func TestTonV3LowerBoundDetector_ZeroFirstSeqnoWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(tonV3MasterchainInfoResponse(0))

	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestTonV3LowerBoundDetector_FetchErrorRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(tonV3MasterchainInfoResponse(66051993)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(66051993), got[protocol.StateBound])
	assert.Equal(t, int64(66051993), got[protocol.BlockBound])
}

func TestTonV3LowerBoundDetector_FetchErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonV3MasterchainInfoRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ton_bounds.NewTonV3LowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
