package sui_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/method-specs/pkg/sui"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// Pruned deployments slide the lowest available checkpoints up continuously;
// re-poll often to keep the bounds close to the real retention boundary.
const suiPeriod = 2 * time.Minute

const (
	suiRetryAttempts = 3
	suiRetryDelay    = 500 * time.Millisecond
)

// SuiLowerBoundDetector reads both bounds from the same GetServiceInfo poll
// the other probes use: lowest_available_checkpoint (checkpoint/transaction
// data) becomes the block bound, lowest_available_checkpoint_objects (object
// data) becomes the state bound. A zero or absent field means the node did
// not report that boundary; the bound is simply skipped for the tick and the
// previously published value stays.
type SuiLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewSuiLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *SuiLowerBoundDetector {
	return &SuiLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *SuiLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	retryPolicy := retrypolicy.NewBuilder[*sui.GetServiceInfoResponse]().
		WithMaxAttempts(suiRetryAttempts).
		WithDelay(suiRetryDelay).
		ReturnLastFailure().
		Build()
	serviceInfo, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (*sui.GetServiceInfoResponse, error) {
		return s.fetchServiceInfo(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the sui service info for upstream '%s': %w", s.upstreamId, err)
	}

	bounds := make([]protocol.LowerBoundData, 0, 2)
	if lowest := serviceInfo.GetLowestAvailableCheckpoint(); lowest > 0 {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(int64(lowest), protocol.BlockBound)) //nolint:gosec // checkpoint sequences are far below int64 max
	}
	if lowestObjects := serviceInfo.GetLowestAvailableCheckpointObjects(); lowestObjects > 0 {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(int64(lowestObjects), protocol.StateBound)) //nolint:gosec // checkpoint sequences are far below int64 max
	}
	return bounds, nil
}

func (s *SuiLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.BlockBound, protocol.StateBound}
}

func (s *SuiLowerBoundDetector) Period() time.Duration {
	return suiPeriod
}

func (s *SuiLowerBoundDetector) fetchServiceInfo(ctx context.Context) (*sui.GetServiceInfoResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	serviceInfo, _, err := specific_helpers.FetchSuiServiceInfo(ctx, s.connector, s.chain)
	return serviceInfo, err
}

var _ lower_bounds.LowerBoundDetector = (*SuiLowerBoundDetector)(nil)
