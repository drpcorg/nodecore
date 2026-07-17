package ripple_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/ripple_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func rippleServerStateResponse(completeLedgers string) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":"1","result":{"state":{"build_version":"2.5.0","complete_ledgers":"%s","validated_ledger":{"seq":105662737}},"status":"success"}}`,
		completeLedgers,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func matchRippleServerStateRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "server_state" && req.RequestType() == protocol.JsonRpc
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

func TestRippleLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.StateBound,
			protocol.BlockBound,
			protocol.UnknownBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 2*time.Minute, detector.Period())
}

func TestRippleLowerBoundDetector_AllowsBoundDecrease(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	var _ lower_bounds.DecreasingBoundDetector = detector
	assert.True(t, detector.AllowsBoundDecrease())
}

func TestRippleLowerBoundDetector_SingleRangeReturnsStateAndBlockBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("105660494-105662737"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(105660494), got[protocol.StateBound])
	assert.Equal(t, int64(105660494), got[protocol.BlockBound])
}

func TestRippleLowerBoundDetector_DisjointRangesUseLastRangeStart(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("32570-1000000,2000000-3000000,105660494-105662737"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(105660494), got[protocol.StateBound])
	assert.Equal(t, int64(105660494), got[protocol.BlockBound])
}

func TestRippleLowerBoundDetector_DecreasingBoundIsReturnedBothTimes(t *testing.T) {
	// an archival node backfilling history: the low edge moves DOWN between
	// two ticks and the detector must report both values as is
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("105660494-105662737")).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("105000000-105662737"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	got := boundsByType(t, result)
	assert.Equal(t, int64(105660494), got[protocol.StateBound])

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	got = boundsByType(t, result)
	assert.Equal(t, int64(105000000), got[protocol.StateBound])
	assert.Equal(t, int64(105000000), got[protocol.BlockBound])
}

func TestRippleLowerBoundDetector_EmptyLedgersWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("empty"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestRippleLowerBoundDetector_EmptyLedgersRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("105660494-105662737")).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("empty"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(105660494), got[protocol.StateBound])
	assert.Equal(t, int64(105660494), got[protocol.BlockBound])
}

func TestRippleLowerBoundDetector_MalformedLedgersFallsBack(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("not-a-range"))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestRippleLowerBoundDetector_BlankLedgersFallsBack(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse(""))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestRippleLowerBoundDetector_ErrorWithCacheRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(rippleServerStateResponse("105660494-105662737")).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(105660494), got[protocol.StateBound])
	assert.Equal(t, int64(105660494), got[protocol.BlockBound])
}

func TestRippleLowerBoundDetector_ErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchRippleServerStateRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ripple_bounds.NewRippleLowerBoundDetector("id", chains.RIPPLE, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
