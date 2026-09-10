package cosmos_bounds_test

import (
	"context"
	"testing"
	"time"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"cosmossdk.io/api/tendermint/types"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/cosmos_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

// grpcHash builds a deterministic 32-byte hash out of a seed, the way the
// gRPC API reports block ids - raw bytes.
func grpcHash(seed byte) []byte {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return raw
}

func matchCosmosGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

func cosmosGrpcLatestBlockBytes(t *testing.T, height int64) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetLatestBlockResponse{
		BlockId: &types.BlockID{Hash: grpcHash(1)},
		SdkBlock: &tendermintv1beta1.Block{
			Header: &tendermintv1beta1.Header{
				Height:      height,
				LastBlockId: &types.BlockID{Hash: grpcHash(2)},
			},
		},
	})
	require.NoError(t, err)
	return data
}

func cosmosGrpcBlockByHeightBytes(t *testing.T, height int64) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetBlockByHeightResponse{
		BlockId: &types.BlockID{Hash: grpcHash(7)},
		SdkBlock: &tendermintv1beta1.Block{
			Header: &tendermintv1beta1.Header{
				Height:      height,
				LastBlockId: &types.BlockID{Hash: grpcHash(8)},
			},
		},
	})
	require.NoError(t, err)
	return data
}

// grpcProbedHeightMatches builds a mock matcher for a GetBlockByHeight probe
// whose height satisfies the predicate. Matchers must never assert, only
// report, so a non-probe request simply doesn't match.
func grpcProbedHeightMatches(predicate func(int64) bool) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != "/cosmos.base.tendermint.v1beta1.Service/GetBlockByHeight" || req.RequestType() != protocol.Grpc {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		var probe tendermintv1beta1.GetBlockByHeightRequest
		if err := proto.Unmarshal(body, &probe); err != nil {
			return false
		}
		return predicate(probe.GetHeight())
	}
}

func grpcPrunedError(code codes.Code, message string) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamErrorResponse(
		protocol.NewInternalUpstreamGrpcRequest("/cosmos.base.tendermint.v1beta1.Service/GetBlockByHeight", nil, chains.GetChain("cosmos-hub").Chain),
		&protocol.GrpcStatus{Code: code, Message: message},
	)
}

// The steady-state path: pruned heights come back as INVALID_ARGUMENT with
// the usual "lowest height is N" message.
func TestCosmosGrpcLowerBoundSearchFindsFirstRetainedHeight(t *testing.T) {
	const retainedFrom = 24000000
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcLatestBlockBytes(t, 25000000)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(grpcProbedHeightMatches(func(h int64) bool { return h < retainedFrom }))).
		Return(grpcPrunedError(codes.InvalidArgument, "could not find results for height #1 (lowest height is 24000000)"))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(grpcProbedHeightMatches(func(h int64) bool { return h >= retainedFrom }))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcBlockByHeightBytes(t, retainedFrom)))

	detector := cosmos_bounds.NewCosmosGrpcLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)

	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, bounds)
	assert.Equal(t, int64(retainedFrom), bounds[0].Bound)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
}

// A node that answers a pruned height with a client-error code but no
// recognizable message is still pruned - the canonical code alone decides.
func TestCosmosGrpcLowerBoundPrunedByCodeAlone(t *testing.T) {
	const retainedFrom = 500
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcLatestBlockBytes(t, 1000)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(grpcProbedHeightMatches(func(h int64) bool { return h < retainedFrom }))).
		Return(grpcPrunedError(codes.NotFound, "no such block"))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(grpcProbedHeightMatches(func(h int64) bool { return h >= retainedFrom }))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcBlockByHeightBytes(t, retainedFrom)))

	detector := cosmos_bounds.NewCosmosGrpcLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)

	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, bounds)
	assert.Equal(t, int64(retainedFrom), bounds[0].Bound)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
}

// On an archive node the binary search narrows all the way down to height 0.
// There is no block 0 on a CometBFT chain, and a real node answers that probe
// with codes.Unknown ("height must be greater than 0, but got 0") - a status
// that is neither pruned-classified nor transient, so without a guard the
// probe would burn the full retry budget (~24 minutes in production) before
// the first bound is published. The detector must not send a probe below
// height 1 at all: the mock deliberately carries no expectation for height 0,
// so such a request fails the test.
func TestCosmosGrpcLowerBoundArchiveNodeNeverProbesHeightZero(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcLatestBlockBytes(t, 8)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(grpcProbedHeightMatches(func(h int64) bool { return h >= 1 }))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcBlockByHeightBytes(t, 1)))

	detector := cosmos_bounds.NewCosmosGrpcLowerBoundDetector(
		"id", chains.GetChain("cosmos-hub").Chain, time.Second, connector,
	)
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)

	bounds, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, bounds)
	assert.Equal(t, int64(1), bounds[0].Bound)
}
