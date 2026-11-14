package eth_labels

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/gas_limit"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	gasProbeContract = "0x53Daa71B04d589429f6d3DF52db123913B818F22"
	gasProbeBytecode = "0x6080604052348015600f57600080fd5b506004361060285760003560e01c806351be4eaa14602d575b600080fd5b60336047565b604051603e91906066565b60405180910390f35b60005a905090565b6000819050919050565b606081604f565b82525050565b6000602082019050607960008301846059565b9291505056fea26469706673582212201c0202887c1afe66974b06ee355dee07542bbc424cf4d1659c91f56c08c3dcc064736f6c63430008130033"
)

type EthGasLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (e *EthGasLabelsDetector) DetectLabels(ctx context.Context) map[string]string {
	if e.chain != chains.MONAD_MAINNET && e.chain != chains.MONAD_TESTNET {
		return e.basicGasDetect(ctx)
	}
	labels := e.basicGasDetect(ctx)
	if gas, ok := labels["extra_gas_limit"]; ok && gas == "600000000" {
		return labels
	}
	return e.monadGasDetect(ctx)
}

func NewEthGasLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthGasLabelsDetector {
	return &EthGasLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
	}
}

func (e *EthGasLabelsDetector) monadGasDetect(ctx context.Context) map[string]string {
	params := []any{
		map[string]any{
			"to":   gasProbeContract,
			"data": "0x51be4eaa",
			"gas":  "0x232AAF80", // 590_000_000
		},
		"latest",
		map[string]any{
			gasProbeContract: map[string]any{
				"code": gasProbeBytecode,
			},
		},
	}
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", params, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a eth_call request of upstream '%s' to detect monad gas labels", e.upstreamId)
		return nil
	}

	resp := e.sendGasRequest(ctx, req)
	if !resp.HasError() {
		log.Error().Err(resp.GetError()).Msgf("unable to get monad eth_call response of upstream '%s' to detect gas labels", e.upstreamId)
		return nil
	}

	if strings.Contains(resp.GetError().Message, "gas limit too high") {
		return map[string]string{
			"gas-limit":       "600000000",
			"extra_gas_limit": "600000000",
		}
	}

	return nil
}

func (e *EthGasLabelsDetector) basicGasDetect(ctx context.Context) map[string]string {
	granted, err := gas_limit.Measure(ctx, e.connector, e.chain, e.internalTimeout)
	if err != nil {
		// an expected outcome on clients that reject the call or cannot run it, so not an error
		log.Debug().Err(err).Msgf("unable to measure the eth_call gas limit of upstream '%s' to detect gas labels", e.upstreamId)
		return nil
	}

	nodeGasLimit := granted.String()
	gasLabels := map[string]string{
		"gas-limit":       nodeGasLimit,
		"extra_gas_limit": nodeGasLimit,
	}
	if gas_limit.GrantsExtraGas(granted) {
		gasLabels["extra_gas_limit"] = "600000000"
	}

	return gasLabels
}

func (e *EthGasLabelsDetector) sendGasRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	ctx, cancel := context.WithTimeout(ctx, e.internalTimeout)
	defer cancel()

	return e.connector.SendRequest(ctx, request)
}

var _ labels.LabelsDetector = (*EthGasLabelsDetector)(nil)
