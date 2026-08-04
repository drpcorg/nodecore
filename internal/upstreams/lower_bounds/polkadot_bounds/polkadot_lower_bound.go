package polkadot_bounds

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// polkadotLowerBoundPeriod mirrors dshackle's PolkadotLowerBoundStateDetector.period().
const polkadotLowerBoundPeriod = 5 * time.Minute

// stateDiscardedHint is dshackle's non-retryable marker. A node that pruned the
// state at a height answers with it, which means "no data here" rather than a
// transient failure - so the search may narrow instead of retrying.
const stateDiscardedHint = "state already discarded for"

var polkadotSupportedBoundTypes = []protocol.LowerBoundType{protocol.StateBound}

// PolkadotLowerBoundDetector binary-searches for the oldest height whose state is
// still served. Polkadot state methods key off a block hash, never a number, so
// each probe costs two calls: resolve the height to a hash, then ask for the
// metadata at that hash.
type PolkadotLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewPolkadotLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *PolkadotLowerBoundDetector {
	return &PolkadotLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithSupportedTypes(
			upstreamId,
			protocol.StateBound,
			polkadotSupportedBoundTypes,
			polkadotLowerBoundPeriod,
		),
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (p *PolkadotLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return p.LowerBoundSearchCalculator.DetectLowerBound(ctx, p.fetchLatestHeight, p.probe)
}

func (p *PolkadotLowerBoundDetector) fetchLatestHeight(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, p.internalTimeout)
	defer cancel()

	header, err := specific_helpers.FetchPolkadotHeader(ctx, p.connector, p.chain)
	if err != nil {
		return 0, err
	}
	height, err := specific_helpers.ParsePolkadotHeight(header.Number)
	if err != nil {
		return 0, err
	}
	return int64(height), nil
}

// probe reports whether the upstream still serves state at the given height. A
// "State already discarded for" error is pruning (false, nil); anything else is
// returned as an error so the calculator retries rather than mistaking an outage
// for pruning.
func (p *PolkadotLowerBoundDetector) probe(ctx context.Context, height int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.internalTimeout)
	defer cancel()

	hash, err := specific_helpers.FetchPolkadotBlockHash(ctx, p.connector, p.chain, hexHeight(height))
	if err != nil {
		if isStateDiscarded(err) {
			return false, nil
		}
		return false, err
	}

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("state_getMetadata", []any{hash}, p.chain)
	if err != nil {
		return false, err
	}
	response := p.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if respErr != nil && strings.Contains(strings.ToLower(respErr.Message), stateDiscardedHint) {
			return false, nil
		}
		return false, respErr
	}
	return len(response.ResponseResult()) > 0, nil
}

func isStateDiscarded(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), stateDiscardedHint)
}

func hexHeight(height int64) string {
	return "0x" + strconv.FormatInt(height, 16)
}

var _ lower_bounds.LowerBoundDetector = (*PolkadotLowerBoundDetector)(nil)
