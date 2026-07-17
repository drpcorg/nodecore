package starknet_bounds

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

// Whether a node keeps full history is a deployment property, not something
// that drifts at runtime, so a long re-probe period is enough; we still
// re-probe every tick instead of trusting a one-time answer forever.
const starknetPeriod = 15 * time.Minute

// starknet JSON-RPC spec: BLOCK_NOT_FOUND. A node that answers this for
// block 1 has pruned its early history.
const starknetBlockNotFoundCode = 24

// The bound we verify: starknet clients expose no earliest-block API, and
// our nodes are archive-grade, so we probe block 1 and publish it as the
// lower bound once the node proves it can serve it.
const starknetVerifiedBound = 1

var starknetEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
	protocol.BlockBound,
}

type StarknetLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewStarknetLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *StarknetLowerBoundDetector {
	return &StarknetLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StarknetLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	err := s.probeEarlyBlock(ctx)
	if err == nil {
		s.lastBound.Store(starknetVerifiedBound)
		return starknetBounds(starknetVerifiedBound), nil
	}
	return s.fallback(err), nil
}

func (s *StarknetLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, starknetEmittedBoundTypes...), protocol.UnknownBound)
}

func (s *StarknetLowerBoundDetector) Period() time.Duration {
	return starknetPeriod
}

// fallback decides what to publish when the probe fails. If a previous tick
// verified the bound, re-emit it so the router keeps using the last known
// good value. Otherwise emit UnknownBound=0 so consumers get an explicit
// "we don't know" signal instead of silence.
func (s *StarknetLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := s.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"starknet upstream '%s' lower-bound probe failed; retaining cached bound=%d",
			s.upstreamId, cached,
		)
		return starknetBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"starknet upstream '%s' lower-bound probe failed and no cache available; emitting UnknownBound",
		s.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func starknetBounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(starknetEmittedBoundTypes))
	for _, bt := range starknetEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
}

// probeEarlyBlock asks for block 1 to verify the node still holds its early
// history. nil means the block is present and the verified bound can be
// published.
func (s *StarknetLowerBoundDetector) probeEarlyBlock(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"starknet_getBlockWithTxHashes",
		[]any{map[string]any{"block_number": starknetVerifiedBound}},
		s.chain,
	)
	if err != nil {
		return err
	}

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if respErr.Code == starknetBlockNotFoundCode {
			// A pruned node: publishing bound 1 would be a lie, and finding
			// the real boundary needs a binary search - a follow-up if we
			// ever run non-archive starknet nodes.
			return fmt.Errorf(
				"starknet upstream '%s' has pruned early history (block %d not found); "+
					"real lower bound needs a binary search: %w",
				s.upstreamId, starknetVerifiedBound, respErr,
			)
		}
		return fmt.Errorf("cannot fetch starknet block %d: %w", starknetVerifiedBound, respErr)
	}

	raw := response.ResponseResult()
	if len(raw) == 0 {
		return fmt.Errorf("starknet upstream '%s' returned an empty body for block %d", s.upstreamId, starknetVerifiedBound)
	}
	var block starknetBlock
	if err := sonic.Unmarshal(raw, &block); err != nil {
		return fmt.Errorf("starknet upstream '%s' block %d unparseable: %w", s.upstreamId, starknetVerifiedBound, err)
	}
	if block.BlockHash == "" {
		return fmt.Errorf("starknet upstream '%s' block %d response has no block_hash", s.upstreamId, starknetVerifiedBound)
	}
	return nil
}

type starknetBlock struct {
	BlockHash string `json:"block_hash"`
}

var _ lower_bounds.LowerBoundDetector = (*StarknetLowerBoundDetector)(nil)
