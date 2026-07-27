package cosmos_bounds

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const cosmosLowerBoundPeriod = 3 * time.Minute

var cosmosSupportedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
}

type CosmosLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewCosmosLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *CosmosLowerBoundDetector {
	return &CosmosLowerBoundDetector{
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

func (c *CosmosLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return c.LowerBoundSearchCalculator.DetectLowerBound(ctx, c.fetchLatestHeight, c.probe)
}

func (c *CosmosLowerBoundDetector) fetchLatestHeight(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	raw, err := specific_helpers.FetchCosmosLatestBlock(ctx, c.connector, c.chain)
	if err != nil {
		return 0, err
	}
	result, err := specific_helpers.ParseCosmosBlock(raw)
	if err != nil {
		return 0, err
	}
	height, err := specific_helpers.ParseDecimalHeight(result.Block.Header.Height)
	if err != nil {
		return 0, fmt.Errorf(
			"cosmos upstream '%s' latest block has an unparseable height '%s': %w",
			c.UpstreamId, result.Block.Header.Height, err,
		)
	}
	return int64(height), nil
}

// probe reports whether the upstream still serves the given height. A pruned
// height comes back as a 4xx with a "could not find results for height" style
// body; anything else (5xx, transport failure) is returned as an error so the
// calculator retries instead of treating an outage as pruning.
func (c *CosmosLowerBoundDetector) probe(ctx context.Context, height int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	response := c.connector.SendRequest(ctx, specific_helpers.CosmosBlockByHeightRequest(c.chain, height))
	if response.HasError() {
		code := response.ResponseCode()
		if code >= 400 && code < 500 {
			return false, nil
		}
		respErr := response.GetError()
		if respErr != nil && isPrunedMessage(respErr.Message) {
			return false, nil
		}
		return false, respErr
	}

	result, err := specific_helpers.ParseCosmosBlock(response.ResponseResult())
	if err != nil {
		return false, err
	}
	parsed, err := specific_helpers.ParseDecimalHeight(result.Block.Header.Height)
	if err != nil || parsed == 0 {
		return false, nil
	}
	return true, nil
}

func isPrunedMessage(message string) bool {
	if message == "" {
		return false
	}
	lower := strings.ToLower(message)
	for _, hint := range prunedHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

var prunedHints = []string{
	"could not find results for height",
	"is not available",
	"is pruned",
	"height is not available",
	"lowest height is",
}

var _ lower_bounds.LowerBoundDetector = (*CosmosLowerBoundDetector)(nil)
