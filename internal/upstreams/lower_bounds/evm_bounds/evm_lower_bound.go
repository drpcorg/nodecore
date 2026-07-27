package evm_bounds

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	evmLowerBoundPeriod = 3 * time.Minute

	evmLowerBoundMaxOffset = 20

	evmZeroAddress = "0x0000000000000000000000000000000000000000"
)

// EvmLowerBoundDetector detects the lower bound of a single data type (block,
// state, tx, receipts or proof) for an EVM upstream. The concrete per-type probe
// logic lives in the sibling *_bound.go files; this file holds the shared wiring:
// construction, the probe dispatcher, and the JSON-RPC call helper. When the
// upstream reports its bounds via eth_capabilities, that answer is used directly
// instead of probing (see capabilities.go).
type EvmLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	capabilities    *EvmCapabilities

	stateOverrideSupport atomic.Int32
}

// WithCapabilities attaches the upstream-shared eth_capabilities cache. Detectors
// without one (nil) keep the pure gold-bound/search behavior.
func (e *EvmLowerBoundDetector) WithCapabilities(capabilities *EvmCapabilities) *EvmLowerBoundDetector {
	e.capabilities = capabilities
	return e
}

func newEvmLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
	boundType protocol.LowerBoundType,
	maxOffset int,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, boundType, []protocol.LowerBoundType{boundType}, maxOffset)
}

func newEvmLowerBoundDetectorWithSupportedTypes(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
	boundType protocol.LowerBoundType,
	supportedTypes []protocol.LowerBoundType,
	maxOffset int,
) *EvmLowerBoundDetector {
	return &EvmLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithOffset(upstreamId, boundType, supportedTypes, evmLowerBoundPeriod, maxOffset),
		connector:                  connector,
		chain:                      chain,
		internalTimeout:            internalTimeout,
	}
}

func (e *EvmLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	if results, ok := e.detectFromCapabilities(ctx); ok {
		return results, nil
	}
	if results, ok := e.detectFromGoldBound(ctx); ok {
		return results, nil
	}
	return e.LowerBoundSearchCalculator.DetectLowerBound(ctx, e.fetchLatestHeight, e.probe)
}

func (e *EvmLowerBoundDetector) probe(ctx context.Context, height int64) (bool, error) {
	switch e.MainBoundType {
	case protocol.StateBound:
		return e.hasState(ctx, height)
	case protocol.BlockBound:
		return e.hasBlock(ctx, height)
	case protocol.TxBound:
		return e.hasTx(ctx, height)
	case protocol.ReceiptsBound:
		return e.hasReceipts(ctx, height)
	case protocol.ProofBound:
		return e.hasProof(ctx, height)
	default:
		return false, fmt.Errorf("unsupported EVM lower-bound type %s", e.MainBoundType.String())
	}
}

func (e *EvmLowerBoundDetector) fetchLatestHeight(ctx context.Context) (int64, error) {
	raw, available, err := e.call(ctx, "eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	if !available {
		return 0, fmt.Errorf("eth_blockNumber unavailable")
	}
	return parseHexInt(raw)
}

func (e *EvmLowerBoundDetector) call(ctx context.Context, method string, params any) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, e.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, e.chain.Chain)
	if err != nil {
		return nil, false, err
	}
	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if isEvmNoDataError(respErr) {
			return nil, false, nil
		}
		return nil, false, respErr
	}
	if isEvmNullResult(response.ResponseResult()) {
		return response.ResponseResult(), false, nil
	}
	return response.ResponseResult(), true, nil
}

var _ lower_bounds.LowerBoundDetector = (*EvmLowerBoundDetector)(nil)
