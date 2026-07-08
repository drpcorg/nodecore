package evm_bounds

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmReceiptsLowerBoundDetector detects the earliest block whose first
// transaction receipt is still retrievable. Like the tx detector it uses the
// offset search to tolerate transaction-less historical blocks.
func NewEvmReceiptsLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetector(upstreamId, chain, internalTimeout, connector, protocol.ReceiptsBound, evmLowerBoundMaxOffset)
}

func (e *EvmLowerBoundDetector) hasReceipts(height int64) (bool, error) {
	txHash, available, err := e.firstTxHash(height)
	if err != nil || !available {
		return available, err
	}
	raw, available, err := e.call("eth_getTransactionReceipt", []any{txHash})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}
