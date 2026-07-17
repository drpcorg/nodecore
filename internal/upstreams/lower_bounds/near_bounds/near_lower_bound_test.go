package near_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/near_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func nearStatusResponse(earliestBlockHeight int64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":"1","result":{"chain_id":"mainnet","sync_info":{"latest_block_height":207365026,"earliest_block_height":%d,"syncing":false}}}`,
		earliestBlockHeight,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func nearStatusResponseWithoutEarliest() protocol.ResponseHolder {
	body := `{"jsonrpc":"2.0","id":"1","result":{"chain_id":"mainnet","sync_info":{"latest_block_height":207365026,"syncing":false}}}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func matchNearStatusRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "status" && req.RequestType() == protocol.JsonRpc
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

func TestNearLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

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

func TestNearLowerBoundDetector_EarliestHeightReturnsStateAndBlockBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponse(207157933))

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(207157933), got[protocol.StateBound])
	assert.Equal(t, int64(207157933), got[protocol.BlockBound])
}

func TestNearLowerBoundDetector_ErrorWithCacheRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponse(207157933)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(207157933), got[protocol.StateBound])
	assert.Equal(t, int64(207157933), got[protocol.BlockBound])
}

func TestNearLowerBoundDetector_ErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestNearLowerBoundDetector_ZeroEarliestHeightFallsBack(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponse(0))

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestNearLowerBoundDetector_AbsentEarliestHeightFallsBack(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponseWithoutEarliest())

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestNearLowerBoundDetector_ZeroEarliestHeightRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponse(207157933)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchNearStatusRequest())).
		Return(nearStatusResponse(0))

	detector := near_bounds.NewNearLowerBoundDetector("id", chains.NEAR, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(207157933), got[protocol.StateBound])
	assert.Equal(t, int64(207157933), got[protocol.BlockBound])
}
