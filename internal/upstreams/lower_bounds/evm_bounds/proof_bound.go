package evm_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// NewEvmProofLowerBoundDetector detects the earliest block for which eth_getProof
// still returns data.
func NewEvmProofLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, protocol.ProofBound, []protocol.LowerBoundType{protocol.ProofBound}, 0)
}

func (e *EvmLowerBoundDetector) hasProof(ctx context.Context, height int64) (bool, error) {
	raw, available, err := e.call(ctx, "eth_getProof", []any{evmZeroAddress, []string{}, evmBlockTag(height)})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}
