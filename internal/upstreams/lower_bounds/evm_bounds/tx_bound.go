package evm_bounds

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmTxLowerBoundDetector detects the earliest block whose first transaction
// is still retrievable. It uses the offset search because historical blocks may
// legitimately have no transactions.
func NewEvmTxLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetector(upstreamId, chain, internalTimeout, connector, protocol.TxBound, evmLowerBoundMaxOffset)
}

func (e *EvmLowerBoundDetector) hasTx(height int64) (bool, error) {
	txHash, available, err := e.firstTxHash(height)
	if err != nil || !available {
		return available, err
	}
	raw, available, err := e.call("eth_getTransactionByHash", []any{txHash})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}
