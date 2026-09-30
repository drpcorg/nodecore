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
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// The epoch floor only moves when an operator reseeds the node's rpc-store,
// and an epoch lasts about a day, so an hourly re-check is plenty. The search
// restarts from the cached bound, so a quiet cycle costs a handful of probes.
const suiEpochPeriod = time.Hour

// SuiEpochLowerBoundDetector finds the earliest epoch GetEpoch still answers.
//
// Epoch metadata lives in the node's rpc-store epochs index, which is seeded
// wherever the index was enabled and never pruned afterwards, so its floor is
// unrelated to either checkpoint watermark GetServiceInfo reports: live
// mainnet nodes serve epochs whose checkpoints are long pruned, by a margin
// that differs per node. Nothing reports that floor, hence the binary search.
// GetEpoch is the only epoch-addressed read, so this bound routes just it.
type SuiEpochLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewSuiEpochLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *SuiEpochLowerBoundDetector {
	return &SuiEpochLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculator(
			upstreamId, protocol.EpochBound, suiEpochPeriod,
		),
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *SuiEpochLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return s.LowerBoundSearchCalculator.DetectLowerBound(ctx, s.fetchLatestEpoch, s.probe)
}

// fetchLatestEpoch is the search's upper end. The current epoch is served
// from live system state rather than the index, so it is always answerable.
func (s *SuiEpochLowerBoundDetector) fetchLatestEpoch(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	serviceInfo, _, err := specific_helpers.FetchSuiServiceInfo(ctx, s.connector, s.chain)
	if err != nil {
		return 0, err
	}
	if serviceInfo.Epoch == nil {
		return 0, fmt.Errorf("sui upstream '%s' service info reports no epoch", s.UpstreamId)
	}
	return int64(serviceInfo.GetEpoch()), nil //nolint:gosec // epoch numbers are far below int64 max
}

// probe reports whether the upstream still serves the given epoch. Below the
// index floor the node answers NOT_FOUND ("Epoch N not found") with no pruned
// marker, so that code alone decides. Any other failure (UNAVAILABLE,
// DEADLINE_EXCEEDED, transport) is returned as an error so the calculator
// retries instead of reading an outage as pruning.
func (s *SuiEpochLowerBoundDetector) probe(ctx context.Context, epoch int64) (bool, error) {
	if epoch < 0 {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	body, err := proto.Marshal(&sui.GetEpochRequest{
		Epoch:    new(uint64(epoch)), //nolint:gosec // guarded above
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"epoch"}},
	})
	if err != nil {
		return false, err
	}
	request := protocol.NewInternalUpstreamGrpcRequest("/sui.rpc.v2.LedgerService/GetEpoch", body, s.chain)

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if grpcStatus, ok := protocol.GrpcStatusFromError(respErr); ok && grpcStatus.Code == codes.NotFound {
			return false, nil
		}
		return false, respErr
	}

	var result sui.GetEpochResponse
	if err := proto.Unmarshal(response.ResponseResult(), &result); err != nil {
		return false, err
	}
	if result.Epoch == nil {
		return false, fmt.Errorf("sui upstream '%s' answered GetEpoch(%d) without an epoch", s.UpstreamId, epoch)
	}
	return true, nil
}

var _ lower_bounds.LowerBoundDetector = (*SuiEpochLowerBoundDetector)(nil)
