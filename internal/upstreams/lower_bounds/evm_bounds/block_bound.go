package evm_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmBlockLowerBoundDetector detects the earliest available block. On an upstream that
// answers pruned blocks with bare headers it detects the start of the contiguous range of
// blocks, ending at the head, that still have their bodies. The logs bound comes from the
// receipts detector: pruned nodes keep headers from genesis but not receipts.
func NewEvmBlockLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(
		upstreamId,
		chain,
		internalTimeout,
		connector,
		protocol.BlockBound,
		[]protocol.LowerBoundType{protocol.BlockBound},
		0,
	)
}

func (e *EvmLowerBoundDetector) hasBlock(ctx context.Context, height int64) (bool, error) {
	raw, available, err := e.call(ctx, "eth_getBlockByNumber", []any{evmBlockTag(height), false})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

// servesBareHeaders reports whether the upstream answers a block whose body it pruned with
// the bare header instead of null (reth with pruned bodies), which hasBlock takes for a block.
// The proof is the block of the chain's gold transaction: it is known to hold one, so a bare
// header there has lost its body. Only that block with its transactions clears a proven
// verdict. Null, a no-data error and a failed request keep it: a proven upstream must not
// fall back to the plain search, which trusts headers and answers 1, while the body search
// stays right for an upstream that turns to answering null.
func (e *EvmLowerBoundDetector) servesBareHeaders(ctx context.Context) bool {
	height, ok := e.goldTxBlock()
	if !ok {
		return false
	}
	block, available, err := e.fetchGoldBlock(ctx, height)
	switch {
	case err != nil || !available:
		// decides nothing
	case block.isBareHeader():
		e.bareHeadersProven.Store(true)
	case len(block.Transactions) > 0:
		e.bareHeadersProven.Store(false)
	}
	return e.bareHeadersProven.Load()
}

// fetchGoldBlock repeats a failed request: without an answer an upstream that serves bare
// headers and is not proven yet gets the plain search and bound 1.
func (e *EvmLowerBoundDetector) fetchGoldBlock(ctx context.Context, height int64) (evmBlockEnvelope, bool, error) {
	for attempt := 1; ; attempt++ {
		block, available, err := e.blockAt(ctx, height)
		if err == nil || attempt >= evmGoldBlockAttempts {
			return block, available, err
		}
		select {
		case <-ctx.Done():
			return evmBlockEnvelope{}, false, ctx.Err()
		case <-time.After(e.goldBlockRetryDelay):
		}
	}
}

// SetGoldBlockRetryDelay overrides the pause between attempts to fetch the gold block.
// Production relies on the default; tests shrink it.
func (e *EvmLowerBoundDetector) SetGoldBlockRetryDelay(delay time.Duration) {
	e.goldBlockRetryDelay = delay
}

// probeBlockBody is the probe of the body search on an upstream that serves bare headers: a
// block with transactions is a hit and a bare header is a miss. A block that really has no
// transactions is ProbeEmpty: it looks the same with and without its body.
func (e *EvmLowerBoundDetector) probeBlockBody(ctx context.Context, height int64) (lower_bounds.ContiguousProbeResult, error) {
	block, available, err := e.blockAt(ctx, height)
	if err != nil || !available {
		return lower_bounds.ProbeMiss, err
	}
	switch {
	case len(block.Transactions) > 0:
		return lower_bounds.ProbeHit, nil
	case block.isBareHeader():
		return lower_bounds.ProbeMiss, nil
	default:
		return lower_bounds.ProbeEmpty, nil
	}
}
