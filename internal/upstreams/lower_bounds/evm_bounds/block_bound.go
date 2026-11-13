package evm_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmBlockLowerBoundDetector detects the earliest available block. The logs bound comes
// from the receipts detector: pruned nodes keep headers from genesis but not receipts.
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

// keepsBareHeaders tells whether the upstream answers a block whose body it pruned with the
// bare header instead of null (reth with pruned bodies), which hasBlock takes for a block.
// The proof is the block of the chain's gold transaction: it is known to hold a transaction,
// so a header there that commits to transactions and carries none has lost its body. Any
// other clean answer clears the verdict. A failed call keeps the previous one: a proven
// upstream must not fall back to the search that trusts headers.
func (e *EvmLowerBoundDetector) keepsBareHeaders(ctx context.Context) bool {
	height, ok := e.goldTxBlock()
	if !ok {
		return false
	}
	block, available, err := e.blockAt(ctx, height)
	if err != nil {
		return e.bareHeaders.Load()
	}
	proven := available && block.bodyPruned()
	e.bareHeaders.Store(proven)
	return proven
}

func (e *EvmLowerBoundDetector) goldTxBlock() (int64, bool) {
	if e.chain == nil || e.chain.LowerBounds == nil {
		return 0, false
	}
	for _, gold := range []*chains.GoldLowerBound{e.chain.LowerBounds.Tx, e.chain.LowerBounds.Receipts} {
		if gold != nil && gold.Hash != "" && gold.Block > 0 {
			return int64(gold.Block), true
		}
	}
	return 0, false
}

// probeBlockBody is the probe of the body search on an upstream that keeps bare headers: a
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
	case block.bodyPruned():
		return lower_bounds.ProbeMiss, nil
	default:
		return lower_bounds.ProbeEmpty, nil
	}
}
