package evm_bounds

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// goldTxBlock is the height of the chain's gold transaction, a block known to hold a
// transaction.
func (e *EvmLowerBoundDetector) goldTxBlock() (int64, bool) {
	if e.chain == nil || e.chain.LowerBounds == nil {
		return 0, false
	}
	gold := e.chain.LowerBounds.Tx
	if gold == nil || gold.Block == 0 {
		return 0, false
	}
	return int64(gold.Block), true
}

// detectFromGoldBound ports dshackle's GoldLowerBounds short-circuit for tx and
// receipts: if the chain has a configured gold hash for this bound type, a single
// RPC probe for that ancient tx/receipt tells us whether the upstream is fully
// archival. A hit resolves the bound to 1 without a binary search; a miss (null
// result, no-data error, or any other error) falls back to the normal search.
func (e *EvmLowerBoundDetector) detectFromGoldBound(ctx context.Context) ([]protocol.LowerBoundData, bool) {
	if e.chain == nil || e.chain.LowerBounds == nil {
		return nil, false
	}
	var method string
	var gold *chains.GoldLowerBound
	switch e.MainBoundType {
	case protocol.TxBound:
		method, gold = "eth_getTransactionByHash", e.chain.LowerBounds.Tx
	case protocol.ReceiptsBound:
		method, gold = "eth_getTransactionReceipt", e.chain.LowerBounds.Receipts
	default:
		return nil, false
	}
	if gold == nil || gold.Hash == "" {
		return nil, false
	}
	raw, available, err := e.call(ctx, method, []any{gold.Hash})
	if err != nil || !available || isEvmNullResult(raw) {
		return nil, false
	}
	return e.LowerBoundResults(1), true
}
