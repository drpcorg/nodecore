package starknet_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/starknet_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func starknetBlockResponse() protocol.ResponseHolder {
	body := `{"jsonrpc":"2.0","id":"1","result":{"status":"ACCEPTED_ON_L1","block_hash":"0x75e00250d4343326f322e370df4c9c73c7be105ad9f532eeb97891a34d9e4a5","block_number":1,"parent_hash":"0x47c3637b57c2b079b93c61539950c17e868a28f46cdef28f88521067f21e943","transactions":["0x2f07a65f9f7a6445b2a0b1fb90ef12f5fd3b94128d06a67712efd3b2f163533"]}}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func starknetBlockNotFoundResponse() protocol.ResponseHolder {
	body := `{"jsonrpc":"2.0","id":"1","error":{"code":24,"message":"Block not found"}}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc)
}

func matchStarknetBlockRequest() func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == "starknet_getBlockWithTxHashes" && req.RequestType() == protocol.JsonRpc
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

func TestStarknetLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

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

func TestStarknetLowerBoundDetector_EarlyBlockPresentReturnsStateAndBlockBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(starknetBlockResponse())

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := boundsByType(t, result)
	assert.Equal(t, int64(1), got[protocol.StateBound])
	assert.Equal(t, int64(1), got[protocol.BlockBound])
}

func TestStarknetLowerBoundDetector_BlockNotFoundWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(starknetBlockNotFoundResponse())

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestStarknetLowerBoundDetector_BlockNotFoundAfterSuccessRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(starknetBlockResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(starknetBlockNotFoundResponse())

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

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

func TestStarknetLowerBoundDetector_TransportErrorWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}

func TestStarknetLowerBoundDetector_TransportErrorAfterSuccessRetainsCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(starknetBlockResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

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

func TestStarknetLowerBoundDetector_UnparseableResultWithoutCacheEmitsUnknownBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := `{"jsonrpc":"2.0","id":"1","result":{"transactions":[]}}`
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchStarknetBlockRequest())).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.JsonRpc))

	detector := starknet_bounds.NewStarknetLowerBoundDetector("id", chains.STARKNET, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.UnknownBound, result[0].Type)
	assert.Equal(t, int64(0), result[0].Bound)
}
