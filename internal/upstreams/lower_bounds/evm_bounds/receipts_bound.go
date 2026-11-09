package evm_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmReceiptsLowerBoundDetector detects the start of the contiguous range of blocks, ending
// at the head, whose receipts are still retrievable. It reports the logs bound too: eth_getLogs
// reads receipts, so a node answers it only where it has them (headers alone say nothing).
func NewEvmReceiptsLowerBoundDetector(
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
		protocol.ReceiptsBound,
		[]protocol.LowerBoundType{protocol.ReceiptsBound, protocol.LogsBound},
		evmLowerBoundMaxOffset,
	)
}

func (e *EvmLowerBoundDetector) hasReceipts(ctx context.Context, height int64) (bool, error) {
	result, err := e.probeReceipts(ctx, height)
	return result == lower_bounds.ProbeHit, err
}

// probeReceipts checks the receipt of the block's last transaction: on OP-stack the first one is
// the L1-info deposit. A block without transactions is ProbeEmpty.
func (e *EvmLowerBoundDetector) probeReceipts(ctx context.Context, height int64) (lower_bounds.ContiguousProbeResult, error) {
	txHash, available, empty, err := e.lastTxHash(ctx, height)
	if err != nil {
		return lower_bounds.ProbeMiss, err
	}
	if empty {
		return lower_bounds.ProbeEmpty, nil
	}
	if !available {
		return lower_bounds.ProbeMiss, nil
	}
	raw, available, err := e.call(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil || !available || isEvmNullResult(raw) {
		return lower_bounds.ProbeMiss, err
	}
	return lower_bounds.ProbeHit, nil
}
