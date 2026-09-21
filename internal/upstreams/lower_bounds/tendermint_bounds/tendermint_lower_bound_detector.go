package tendermint_bounds

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

const tendermintLowerBoundPeriod = 3 * time.Minute

type TendermintLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewTendermintLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *TendermintLowerBoundDetector {
	return &TendermintLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TendermintLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	ctx, cancel := context.WithTimeout(ctx, t.internalTimeout)
	defer cancel()

	status, err := specific_helpers.FetchTendermintStatus(ctx, t.connector, t.chain)
	if err != nil {
		return nil, err
	}
	bound, err := specific_helpers.ParseDecimalHeight(status.SyncInfo.EarliestBlockHeight)
	if err != nil {
		return nil, fmt.Errorf(
			"tendermint upstream '%s' returned an unparseable earliest_block_height '%s': %w",
			t.upstreamId, status.SyncInfo.EarliestBlockHeight, err,
		)
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(bound), protocol.StateBound),
	}, nil
}

func (t *TendermintLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (t *TendermintLowerBoundDetector) Period() time.Duration {
	return tendermintLowerBoundPeriod
}

var _ lower_bounds.LowerBoundDetector = (*TendermintLowerBoundDetector)(nil)
