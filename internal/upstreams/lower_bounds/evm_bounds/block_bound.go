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

// detectBlockBound picks the search for the block bound. An upstream that answers a block
// whose body it pruned with the bare header instead of null (reth with pruned bodies) gets
// the body search: the plain one takes its headers for blocks. Two blocks can prove such an
// upstream: the gold one and, when that one decides nothing, a block at the plain bound.
// A proven verdict stays for the cycles that prove nothing, and only the gold block with its
// transactions clears it: a proven upstream must not fall back to the plain search, while
// the body search stays right for an upstream that turns to answering null.
func (e *EvmLowerBoundDetector) detectBlockBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	goldHasBody := e.judgeByGoldBlock(ctx)
	if e.bareHeadersProven.Load() {
		return e.detectBodyBound(ctx)
	}
	results, err := e.LowerBoundSearchCalculator.DetectLowerBound(ctx, e.fetchLatestHeight, e.probe)
	if err != nil || goldHasBody {
		return results, err
	}
	if e.bareHeaderAtPlainBound(ctx, results[0].Bound) {
		e.bareHeadersProven.Store(true)
		return e.detectBodyBound(ctx)
	}
	return results, nil
}

func (e *EvmLowerBoundDetector) detectBodyBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return e.DetectContiguousLowerBoundWithOffset(ctx, e.fetchLatestHeight, e.probeBlockBody, evmLowerBoundMaxOffset)
}

// judgeByGoldBlock is the first proof, the block of the chain's gold transaction: it is known
// to hold one, so a bare header there has lost its body. Null, a no-data error and a failed
// request decide nothing. hasBody reports that block with its transactions: the upstream
// serves bodies that far back, so the blocks at the plain bound are not asked.
func (e *EvmLowerBoundDetector) judgeByGoldBlock(ctx context.Context) (hasBody bool) {
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
		return true
	}
	return false
}

// bareHeaderAtPlainBound is the second proof, for an upstream whose gold block decides
// nothing. A node restored from a snapshot answers null below the snapshot, the gold block
// included, and bare headers from it on, so the plain search stops at the first header. The
// blocks from the plain bound up are asked, evmLowerBoundMaxOffset past it at most. A block
// with transactions ends the proof: bodies start where the plain search says. A bare header
// proves the upstream only when it used gas too, as a false verdict lifts the bound of an
// archive node for good. The root alone is not enough: an empty block carries the keccak of
// the empty string on klaytn and the root of the previous block on etherlink. Nor is gas
// alone: on cosmos chains a block without EVM transactions reports the gas of the cosmos
// ones, with the empty trie root.
func (e *EvmLowerBoundDetector) bareHeaderAtPlainBound(ctx context.Context, bound int64) bool {
	for height := bound; height <= bound+evmLowerBoundMaxOffset; height++ {
		block, available, err := e.blockAt(ctx, height)
		switch {
		case err != nil || !available:
			// decides nothing
		case len(block.Transactions) > 0:
			return false
		case block.isBareHeader() && block.usedGas():
			return true
		}
	}
	return false
}

// fetchGoldBlock repeats a failed request: without an answer an upstream that serves bare
// headers and is not proven yet depends on the blocks at the plain bound, and where those
// are empty it gets the plain search and bound 1.
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
