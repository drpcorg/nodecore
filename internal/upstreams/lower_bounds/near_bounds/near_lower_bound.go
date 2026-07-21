package near_bounds

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// Non-archival nodes garbage-collect a sliding ~5-epoch window, so the
// earliest available block moves at roughly one block per second; re-poll
// often to keep the bound close to the real GC boundary.
const nearPeriod = 3 * time.Minute

const (
	nearRetryAttempts = 3
	nearRetryDelay    = 500 * time.Millisecond
)

var errNearNoEarliestHeight = errors.New("near node reported no earliest_block_height")

// NearLowerBoundDetector reads earliest_block_height from the `status`
// response - nearcore publishes its GC boundary directly, one RPC per
// refresh. On any error the detector returns (nil, err): the processor
// logs it, skips the tick, and the previously cached bound stays in place.
type NearLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewNearLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *NearLowerBoundDetector {
	return &NearLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (n *NearLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	retryPolicy := retrypolicy.NewBuilder[*nearStatus]().
		WithMaxAttempts(nearRetryAttempts).
		WithDelay(nearRetryDelay).
		ReturnLastFailure().
		Build()
	status, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (*nearStatus, error) {
		return n.fetchStatus(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch near status for upstream '%s': %w", n.upstreamId, err)
	}

	bound := status.SyncInfo.EarliestBlockHeight
	if bound <= 0 {
		// Zero or absent means the node did not report its GC boundary,
		// not that the full history is available.
		return nil, errNearNoEarliestHeight
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(bound, protocol.StateBound),
		protocol.NewLowerBoundDataNow(bound, protocol.BlockBound),
	}, nil
}

func (n *NearLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound, protocol.BlockBound}
}

func (n *NearLowerBoundDetector) Period() time.Duration {
	return nearPeriod
}

func (n *NearLowerBoundDetector) fetchStatus(ctx context.Context) (*nearStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, n.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("status", []any{}, n.chain)
	if err != nil {
		return nil, err
	}

	response := n.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, fmt.Errorf("near upstream '%s' status returned an empty body", n.upstreamId)
	}
	var status nearStatus
	if err := sonic.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("near upstream '%s' status unparseable: %w", n.upstreamId, err)
	}
	return &status, nil
}

type nearStatus struct {
	SyncInfo nearSyncInfo `json:"sync_info"`
}

type nearSyncInfo struct {
	EarliestBlockHeight int64 `json:"earliest_block_height"`
}

var _ lower_bounds.LowerBoundDetector = (*NearLowerBoundDetector)(nil)
