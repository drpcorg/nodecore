package celestia_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/celestia_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const celestiaPeriod = 5 * time.Minute

// CelestiaLowerBoundDetector reports the lowest height the upstream still serves,
// via header.Tail (one RPC per refresh). Headers and shwap/blob data below Tail
// are pruned away (the DA sampling window). Nodes older than celestia-node
// v0.28 don't expose header.Tail; the detector then errors every tick and the
// upstream keeps no bound, i.e. it is treated as a full archive.
type CelestiaLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewCelestiaLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *CelestiaLowerBoundDetector {
	return &CelestiaLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CelestiaLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	bound, err := c.fetchTailHeight(ctx)
	if err != nil {
		return nil, err
	}
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(bound, protocol.BlockBound),
	}, nil
}

func (c *CelestiaLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.BlockBound}
}

func (c *CelestiaLowerBoundDetector) Period() time.Duration {
	return celestiaPeriod
}

func (c *CelestiaLowerBoundDetector) fetchTailHeight(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"header.Tail",
		[]interface{}{},
		c.chain,
	)
	if err != nil {
		return 0, err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}

	header, err := celestia_validations.ParseExtendedHeader(response.ResponseResult())
	if err != nil {
		return 0, err
	}
	height, err := header.Height()
	if err != nil {
		return 0, err
	}
	return int64(height), nil
}

var _ lower_bounds.LowerBoundDetector = (*CelestiaLowerBoundDetector)(nil)
