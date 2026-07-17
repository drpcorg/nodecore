package near_bounds

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// Non-archival nodes garbage-collect a sliding ~5-epoch window, so the
// earliest available block moves at roughly one block per second; re-poll
// often to keep the bound close to the real GC boundary.
const nearPeriod = 3 * time.Minute

var errNearNoEarliestHeight = errors.New("near node reported no earliest_block_height")

var nearEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
	protocol.BlockBound,
}

type NearLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
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
	status, err := n.fetchStatus(ctx)
	if err != nil {
		return n.fallback(fmt.Errorf("cannot fetch node status: %w", err)), nil
	}

	bound := status.SyncInfo.EarliestBlockHeight
	if bound <= 0 {
		// Zero or absent means the node did not report its GC boundary,
		// not that the full history is available.
		return n.fallback(errNearNoEarliestHeight), nil
	}
	n.lastBound.Store(bound)

	return nearBounds(bound), nil
}

func (n *NearLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, nearEmittedBoundTypes...), protocol.UnknownBound)
}

func (n *NearLowerBoundDetector) Period() time.Duration {
	return nearPeriod
}

// fallback decides what to publish when the calculation cannot complete.
// If a previous tick produced a bound, re-emit it so the router keeps using
// the last known good value. Otherwise emit UnknownBound=0 so consumers get
// an explicit "we don't know" signal instead of silence.
func (n *NearLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := n.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"near upstream '%s' lower-bound calculation failed; retaining cached bound=%d",
			n.upstreamId, cached,
		)
		return nearBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"near upstream '%s' lower-bound calculation failed and no cache available; emitting UnknownBound",
		n.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func nearBounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(nearEmittedBoundTypes))
	for _, bt := range nearEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
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
