package evm_bounds

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	stateCheckerAddress  = "0x1111111111111111111111111111111111111111"
	stateCheckerCallData = "0x1eaf190c"
	stateCheckerBytecode = "0x6080604052348015600e575f5ffd5b50600436106026575f3560e01c80631eaf190c14602a575b5f5ffd5b60306044565b604051603b91906078565b60405180910390f35b5f5f73ffffffffffffffffffffffffffffffffffffffff1631905090565b5f819050919050565b6072816062565b82525050565b5f60208201905060895f830184606b565b9291505056fea2646970667358221220251f5b4d2ed1abe77f66fde198a57ada08562dc3b0afbc6bac0261d1bf516b5d64736f6c634300081e0033"
)

type evmStateOverrideSupport int32

const (
	evmStateOverrideUnknown evmStateOverrideSupport = iota
	evmStateOverrideSupported
	evmStateOverrideUnsupported
)

// NewEvmStateLowerBoundDetector detects the earliest block with available state.
// The same search satisfies the trace lower bound, so both types are reported.
func NewEvmStateLowerBoundDetector(
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
		protocol.StateBound,
		[]protocol.LowerBoundType{protocol.StateBound, protocol.TraceBound},
		0,
	)
}

func (e *EvmLowerBoundDetector) hasState(height int64) (bool, error) {
	if e.supportsStateOverride() {
		available, err := e.hasStateWithOverride(height)
		if err == nil && available {
			return true, nil
		}
		// Match dshackle behavior: state override is preferred, but a failed
		// per-block override probe falls back to eth_getBalance.
	}
	return e.hasStateWithBalance(height)
}

func (e *EvmLowerBoundDetector) hasStateWithOverride(height int64) (bool, error) {
	raw, available, err := e.call("eth_call", stateOverrideParams(evmBlockTag(height)))
	if err != nil || !available {
		return available, err
	}
	return !isEvmEmptyHexResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasStateWithBalance(height int64) (bool, error) {
	raw, available, err := e.call("eth_getBalance", []any{evmZeroAddress, evmBlockTag(height)})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

func (e *EvmLowerBoundDetector) supportsStateOverride() bool {
	switch evmStateOverrideSupport(e.stateOverrideSupport.Load()) {
	case evmStateOverrideSupported:
		return true
	case evmStateOverrideUnsupported:
		return false
	}

	raw, available, err := e.call("eth_call", stateOverrideParams("latest"))
	if err == nil && available && !isEvmEmptyHexResult(raw) {
		e.stateOverrideSupport.Store(int32(evmStateOverrideSupported))
		return true
	}
	e.stateOverrideSupport.Store(int32(evmStateOverrideUnsupported))
	return false
}

func stateOverrideParams(block string) []any {
	return []any{
		map[string]any{
			"to":   stateCheckerAddress,
			"data": stateCheckerCallData,
		},
		block,
		map[string]any{
			stateCheckerAddress: map[string]any{
				"code": stateCheckerBytecode,
			},
		},
	}
}
