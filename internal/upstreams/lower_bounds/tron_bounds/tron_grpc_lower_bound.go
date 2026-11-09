package tron_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// TronGrpcLowerBoundDetector is the gRPC twin of TronLowerBoundDetector -
// the same retention search probing protocol.Wallet/GetBlock (detail false).
type TronGrpcLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewTronGrpcLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *TronGrpcLowerBoundDetector {
	return &TronGrpcLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithSupportedTypes(
			upstreamId,
			protocol.BlockBound,
			tronSupportedBoundTypes,
			tronLowerBoundPeriod,
		),
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TronGrpcLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	bounds, err := t.LowerBoundSearchCalculator.DetectLowerBound(ctx, t.fetchLatestHeight, t.probe)
	if err != nil {
		return nil, err
	}
	return expandTronBounds(bounds), nil
}

func (t *TronGrpcLowerBoundDetector) fetchLatestHeight(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, t.internalTimeout)
	defer cancel()

	raw, err := specific_helpers.FetchTronGrpcNowBlock(ctx, t.connector, t.chain)
	if err != nil {
		return 0, err
	}
	block, err := specific_helpers.ParseTronGrpcBlock(raw)
	if err != nil {
		return 0, fmt.Errorf("tron upstream '%s' latest block unparseable: %w", t.UpstreamId, err)
	}
	number := block.GetBlockHeader().GetRawData().GetNumber()
	if number <= 0 {
		return 0, fmt.Errorf("tron upstream '%s' returned non-positive latest block number %d", t.UpstreamId, number)
	}
	return number, nil
}

// probe reports whether the upstream still serves the given height. java-tron
// answers a missing block with an empty BlockExtention and OK status - the
// gRPC form of the HTTP API's `{}` - so an empty block id means pruned. Any
// error status is an outage and is returned so the calculator retries.
func (t *TronGrpcLowerBoundDetector) probe(ctx context.Context, height int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, t.internalTimeout)
	defer cancel()

	request, err := specific_helpers.TronGrpcBlockByNumRequest(t.chain, height)
	if err != nil {
		return false, err
	}
	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return false, response.GetError()
	}
	block, err := specific_helpers.ParseTronGrpcBlock(response.ResponseResult())
	if err != nil {
		return false, fmt.Errorf("tron upstream '%s' GetBlock body unparseable: %w", t.UpstreamId, err)
	}
	return len(block.GetBlockid()) > 0, nil
}

var _ lower_bounds.LowerBoundDetector = (*TronGrpcLowerBoundDetector)(nil)
