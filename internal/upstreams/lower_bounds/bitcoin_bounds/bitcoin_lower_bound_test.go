package bitcoin_bounds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/bitcoin_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func bitcoinInfoResponse(pruned bool, pruneHeight int64) protocol.ResponseHolder {
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":"1","result":{"chain":"main","blocks":850000,"pruned":%t,"pruneheight":%d}}`,
		pruned, pruneHeight,
	)
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func bitcoinUnprunedResponse() protocol.ResponseHolder {
	body := `{"jsonrpc":"2.0","id":"1","result":{"chain":"main","blocks":850000,"pruned":false}}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func matchBitcoinInfoRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "getblockchaininfo" && req.RequestType() == protocol.JsonRpc
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

func TestBitcoinLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.BlockBound,
			protocol.TxBound,
			protocol.UnknownBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 15*time.Minute, detector.Period())
}

func TestBitcoinLowerBoundDetector_UnprunedReturnsOne(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(bitcoinUnprunedResponse())

	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(1), got[protocol.BlockBound])
	assert.Equal(t, int64(1), got[protocol.TxBound])
}

func TestBitcoinLowerBoundDetector_PrunedReturnsPruneHeight(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(bitcoinInfoResponse(true, 812345))

	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(812345), got[protocol.BlockBound])
	assert.Equal(t, int64(812345), got[protocol.TxBound])
}

func TestBitcoinLowerBoundDetector_ErrorWithCacheRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(bitcoinInfoResponse(true, 812345)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(812345), got[protocol.BlockBound])
	assert.Equal(t, int64(812345), got[protocol.TxBound])
}

func TestBitcoinLowerBoundDetector_ErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestBitcoinLowerBoundDetector_PrunedWithoutPruneHeightFallsBack(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBitcoinInfoRequest())).
		Return(bitcoinInfoResponse(true, 0))

	detector := bitcoin_bounds.NewBitcoinLowerBoundDetector("id", chains.BITCOIN, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
