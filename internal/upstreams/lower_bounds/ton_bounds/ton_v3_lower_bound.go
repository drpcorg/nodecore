package ton_bounds

import (
	"context"
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

// The v3 indexer advertises its history floor directly (first.seqno of
// masterchainInfo) and it moves only when the operator re-backfills or the
// indexer prunes, so a moderate re-poll period is enough.
const tonV3Period = 3 * time.Minute

// The v3 index serves both tx/block data and account states.
var tonV3EmittedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
	protocol.BlockBound,
}

type TonV3LowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewTonV3LowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *TonV3LowerBoundDetector {
	return &TonV3LowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonV3LowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	first, err := t.fetchFirstSeqno(ctx)
	if err != nil {
		return t.fallback(err), nil
	}
	if first == 0 {
		return t.fallback(fmt.Errorf("ton v3 upstream '%s' advertises no history floor", t.upstreamId)), nil
	}
	t.lastBound.Store(first)
	return tonV3Bounds(first), nil
}

func (t *TonV3LowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, tonV3EmittedBoundTypes...), protocol.UnknownBound)
}

func (t *TonV3LowerBoundDetector) Period() time.Duration {
	return tonV3Period
}

// AllowsBoundDecrease opts this detector out of the processor's monotonic
// filter: the operator can re-backfill the index deeper at any time, so
// first.seqno legally DECREASES.
func (t *TonV3LowerBoundDetector) AllowsBoundDecrease() bool {
	return true
}

// fallback decides what to publish when the probe fails or the node
// advertises no floor. If a previous tick produced a bound, re-emit it so
// the router keeps using the last known good value. Otherwise emit
// UnknownBound=0 so consumers get an explicit "we don't know" signal
// instead of silence.
func (t *TonV3LowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := t.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"ton v3 upstream '%s' lower-bound detection failed; retaining cached bound=%d",
			t.upstreamId, cached,
		)
		return tonV3Bounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"ton v3 upstream '%s' lower-bound detection failed and no cache available; emitting UnknownBound",
		t.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func tonV3Bounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(tonV3EmittedBoundTypes))
	for _, bt := range tonV3EmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
}

func (t *TonV3LowerBoundDetector) fetchFirstSeqno(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, t.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/api/v3/masterchainInfo", nil, t.chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, fmt.Errorf("cannot fetch ton v3 masterchain info: %w", response.GetError())
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return 0, fmt.Errorf("ton v3 upstream '%s' returned an empty masterchainInfo body", t.upstreamId)
	}
	var info tonV3MasterchainInfo
	if err := sonic.Unmarshal(raw, &info); err != nil {
		return 0, fmt.Errorf("ton v3 upstream '%s' masterchainInfo unparseable: %w", t.upstreamId, err)
	}
	return int64(info.First.Seqno), nil //nolint:gosec // masterchain seqno fits int64
}

type tonV3MasterchainInfo struct {
	First struct {
		Seqno uint64 `json:"seqno"`
	} `json:"first"`
}

var _ lower_bounds.LowerBoundDetector = (*TonV3LowerBoundDetector)(nil)
var _ lower_bounds.DecreasingBoundDetector = (*TonV3LowerBoundDetector)(nil)
