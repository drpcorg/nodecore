package celestia_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// Pruned nodes slide their tail up continuously; re-poll often to keep the
// bound close to the real retention boundary.
const celestiaPeriod = 2 * time.Minute

const (
	celestiaRetryAttempts = 3
	celestiaRetryDelay    = 500 * time.Millisecond
)

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
	retryPolicy := retrypolicy.NewBuilder[int64]().
		WithMaxAttempts(celestiaRetryAttempts).
		WithDelay(celestiaRetryDelay).
		ReturnLastFailure().
		Build()
	bound, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (int64, error) {
		return c.fetchTailHeight(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the celestia tail for upstream '%s': %w", c.upstreamId, err)
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

	header, err := specific_helpers.ParseCelestiaExtendedHeader(response.ResponseResult())
	if err != nil {
		return 0, err
	}
	height, err := header.Height()
	if err != nil {
		return 0, err
	}
	return int64(height), nil //nolint:gosec // celestia heights are far below int64 max
}

var _ lower_bounds.LowerBoundDetector = (*CelestiaLowerBoundDetector)(nil)
