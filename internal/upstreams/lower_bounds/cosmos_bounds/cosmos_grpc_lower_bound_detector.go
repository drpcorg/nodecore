package cosmos_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"google.golang.org/grpc/codes"
)

// CosmosGrpcLowerBoundDetector is the gRPC twin of CosmosLowerBoundDetector -
// the same retention search probing
// cosmos.base.tendermint.v1beta1.Service/GetBlockByHeight.
type CosmosGrpcLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewCosmosGrpcLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *CosmosGrpcLowerBoundDetector {
	return &CosmosGrpcLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithSupportedTypes(
			upstreamId,
			protocol.StateBound,
			cosmosSupportedBoundTypes,
			cosmosLowerBoundPeriod,
		),
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CosmosGrpcLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return c.LowerBoundSearchCalculator.DetectLowerBound(ctx, c.fetchLatestHeight, c.probe)
}

func (c *CosmosGrpcLowerBoundDetector) fetchLatestHeight(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	raw, err := specific_helpers.FetchCosmosGrpcLatestBlock(ctx, c.connector, c.chain)
	if err != nil {
		return 0, err
	}
	result, err := specific_helpers.ParseCosmosGrpcBlock(raw)
	if err != nil {
		return 0, err
	}
	height, _ := specific_helpers.CosmosGrpcBlockHeader(result)
	if height <= 0 {
		return 0, fmt.Errorf("cosmos upstream '%s' latest block reports no height", c.UpstreamId)
	}
	return height, nil
}

// probe reports whether the upstream still serves the given height. A pruned
// height comes back as a client-error status - typically INVALID_ARGUMENT
// with a "lowest height is N" style message - so a canonical client-error
// code or a recognizable message means pruned; anything else (UNAVAILABLE,
// DEADLINE_EXCEEDED, transport failure) is returned as an error so the
// calculator retries instead of treating an outage as pruning.
func (c *CosmosGrpcLowerBoundDetector) probe(ctx context.Context, height int64) (bool, error) {
	// CometBFT chains have no block 0 (initial_height >= 1), and a node
	// answers a height-0 probe with codes.Unknown ("height must be greater
	// than 0") - unclassifiable as pruned, so without this guard the search's
	// genesis-floor probe would burn the whole retry budget as an outage.
	if height < 1 {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	request, err := specific_helpers.CosmosGrpcBlockByHeightRequest(c.chain, height)
	if err != nil {
		return false, err
	}
	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if grpcStatus, ok := protocol.GrpcStatusFromError(respErr); ok {
			switch grpcStatus.Code {
			case codes.InvalidArgument, codes.NotFound, codes.OutOfRange:
				return false, nil
			}
			if isPrunedMessage(grpcStatus.Message) {
				return false, nil
			}
		}
		return false, respErr
	}

	result, err := specific_helpers.ParseCosmosGrpcBlockByHeight(response.ResponseResult())
	if err != nil {
		return false, err
	}
	parsedHeight, _ := specific_helpers.CosmosGrpcBlockHeader(result)
	return parsedHeight > 0, nil
}

var _ lower_bounds.LowerBoundDetector = (*CosmosGrpcLowerBoundDetector)(nil)
