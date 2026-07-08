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
// construction, the probe dispatcher, and the JSON-RPC call helper.
type EvmLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration

	stateOverrideSupport atomic.Int32
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

func (e *EvmLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	if results, ok := e.detectFromGoldBound(); ok {
		return results, nil
	}
	return e.LowerBoundSearchCalculator.DetectLowerBound(e.fetchLatestHeight, e.probe)
}

func (e *EvmLowerBoundDetector) probe(height int64) (bool, error) {
	switch e.MainBoundType {
	case protocol.StateBound:
		return e.hasState(height)
	case protocol.BlockBound:
		return e.hasBlock(height)
	case protocol.TxBound:
		return e.hasTx(height)
	case protocol.ReceiptsBound:
		return e.hasReceipts(height)
	case protocol.ProofBound:
		return e.hasProof(height)
	default:
		return false, fmt.Errorf("unsupported EVM lower-bound type %s", e.MainBoundType.String())
	}
}

func (e *EvmLowerBoundDetector) fetchLatestHeight() (int64, error) {
	raw, available, err := e.call("eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	if !available {
		return 0, fmt.Errorf("eth_blockNumber unavailable")
	}
	return parseHexInt(raw)
}

func (e *EvmLowerBoundDetector) call(method string, params any) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
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
