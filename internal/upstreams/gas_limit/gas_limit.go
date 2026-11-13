// Package gas_limit measures the eth_call gas budget an EVM node grants.
package gas_limit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	// init code of a contract whose constructor returns the gas it has left:
	// GAS PUSH1 0 MSTORE PUSH1 32 PUSH1 0 RETURN
	gasLeftInitCode = "0x5a60005260206000f3"
	// the budget the probe asks for; some clients reject anything above it
	requestedGasHex = "0x23C34600"
	requestedGas    = 600_000_000
	// what a contract creation costs before the first opcode of the init code runs
	creationOverhead  = 53_124
	extraGasThreshold = 590_000_000
)

// Measure asks the node for a 600M eth_call budget and returns what it granted.
// An error means the node gave no usable reading; it says nothing about the node's cap.
func Measure(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
	timeout time.Duration,
) (*big.Int, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"data": gasLeftInitCode,
				"gas":  requestedGasHex,
			},
			"latest",
		},
		chain,
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	result, err := response.ResponseResultString()
	if err != nil {
		return nil, err
	}

	hexValue := strings.TrimPrefix(strings.ToLower(result), "0x")
	if hexValue == "" {
		return nil, errors.New("the node returned an empty result")
	}
	gasLeft, ok := new(big.Int).SetString(hexValue, 16)
	if !ok {
		return nil, fmt.Errorf("the node returned a result that is not a hex number: %s", result)
	}
	// a node that honours the request has already spent the creation overhead, so a value
	// that is not below the request is an echo of it or not a gas value at all
	if gasLeft.Cmp(big.NewInt(requestedGas)) >= 0 {
		return nil, fmt.Errorf("the node returned %s, which is not below the requested %d", gasLeft, requestedGas)
	}

	return gasLeft.Add(gasLeft, big.NewInt(creationOverhead)), nil
}

// GrantsExtraGas reports whether a granted budget counts as extra gas.
func GrantsExtraGas(granted *big.Int) bool {
	return granted.Cmp(big.NewInt(extraGasThreshold)) > 0
}
