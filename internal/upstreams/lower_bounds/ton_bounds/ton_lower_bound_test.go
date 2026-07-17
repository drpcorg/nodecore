package ton_bounds_test

import (
	"context"
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

func tonLookupBlockResponse() protocol.ResponseHolder {
	body := `{"ok":true,"result":{"@type":"blocks.header","id":{"@type":"ton.blockIdExt","workchain":-1,"shard":"-9223372036854775808","seqno":1,"root_hash":"VpWY...","file_hash":"pOu2..."},"global_id":-239,"gen_utime":1573821854},"@extra":"1626-1-2"}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func tonNotInDbResponse() protocol.ResponseHolder {
	body := `{"ok":false,"error":"LITE_SERVER_NOTREADY: cannot compute block with specified transaction: lt not in db","code":500,"@extra":"1626-1-3"}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 500, protocol.Rest)
}

func matchTonLookupBlockRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != "GET#/lookupBlock" || req.RequestType() != protocol.Rest {
			return false
		}
		restReq, ok := req.(*protocol.UpstreamRestRequest)
		if !ok || restReq.RequestParams() == nil {
			return false
		}
		query := restReq.RequestParams().QueryParams
		return len(query["workchain"]) == 1 && query["workchain"][0] == "-1" &&
			len(query["shard"]) == 1 && query["shard"][0] == "-9223372036854775808" &&
			len(query["seqno"]) == 1 && query["seqno"][0] == "1"
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

func TestTonLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.StateBound,
			protocol.BlockBound,
			protocol.UnknownBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 15*time.Minute, detector.Period())
}

func TestTonLowerBoundDetector_EarlyBlockPresentReturnsStateAndBlockBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(tonLookupBlockResponse())

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(1), got[protocol.StateBound])
	assert.Equal(t, int64(1), got[protocol.BlockBound])
}

func TestTonLowerBoundDetector_NotInDbWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(tonNotInDbResponse())

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestTonLowerBoundDetector_NotInDbAfterSuccessClearsCacheAndEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(tonLookupBlockResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(tonNotInDbResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	// A definitive "not in db" overrides the cached verified bound.
	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)

	// And the cache is gone: a later transport error has nothing to fall
	// back to, so UnknownBound again rather than a resurrected 1.
	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestTonLowerBoundDetector_TransportErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestTonLowerBoundDetector_TransportErrorAfterSuccessRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(tonLookupBlockResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)

	result, err = detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(1), got[protocol.StateBound])
	assert.Equal(t, int64(1), got[protocol.BlockBound])
}

func TestTonLowerBoundDetector_OkFalseEnvelopeNotInDbEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// Some deployments front the liteserver with a proxy that flattens the
	// status to 200 while keeping the toncenter error envelope.
	body := `{"ok":false,"error":"LITE_SERVER_NOTREADY: seqno not in db","code":500}`
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTonLookupBlockRequest())).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest))

	detector := ton_bounds.NewTonLowerBoundDetector("id", chains.TON, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
