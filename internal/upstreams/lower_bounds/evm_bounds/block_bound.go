package evm_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmBlockLowerBoundDetector detects the earliest available block. The same
// search satisfies the logs lower bound (logs live on blocks), so both types are
// reported.
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
		[]protocol.LowerBoundType{protocol.BlockBound, protocol.LogsBound},
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
