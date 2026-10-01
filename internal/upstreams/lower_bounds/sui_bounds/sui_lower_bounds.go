package sui_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/sui"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// Pruned deployments slide the lowest available checkpoint up continuously;
// re-poll often to keep the bound close to the real retention boundary.
const suiPeriod = 2 * time.Minute

const (
	suiRetryAttempts = 3
	suiRetryDelay    = 500 * time.Millisecond
)

// SuiLowerBoundDetector reads the checkpoint floor from the same
// GetServiceInfo poll the other probes use. lowest_available_checkpoint is
// the earliest checkpoint whose summary, transactions, effects and events the
// node still serves, i.e. the floor every checkpoint-addressed read
// (GetCheckpoint, ListCheckpoints, ListTransactions, ListEvents) is checked
// against - the block bound.
//
// lowest_available_checkpoint_objects is ignored on purpose. Since sui-node
// v1.66 the node folds the object-version floor into
// lowest_available_checkpoint and echoes the same number into the objects
// field, which is deprecated; and no Sui gRPC read addresses state by
// checkpoint, so there is nothing a separate state bound could route.
//
// sui-node always sets the field and reports an explicit 0 when nothing has
// been pruned, so 0 means archive and is published as 1 (the predictor's
// archive value). Only an absent field means the node did not report the
// floor; then the bound is skipped for the tick and the previously published
// value stays.
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

	if serviceInfo.LowestAvailableCheckpoint == nil {
		return nil, nil
	}
	lowest := max(int64(serviceInfo.GetLowestAvailableCheckpoint()), 1) //nolint:gosec // checkpoint sequences are far below int64 max
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(lowest, protocol.BlockBound),
	}, nil
}

func (s *SuiLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.BlockBound}
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
