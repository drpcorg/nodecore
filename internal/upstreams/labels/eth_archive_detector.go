package labels

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	ArbitrumNitroBlock   = "0x152DD47"
	OptimismBedrockBlock = "0x645C277"
	EvmosGenesisBlock    = "0xe54d"
	EarliestBlock        = "0x2710"
)

type EthArchiveLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	internalTimeout time.Duration
	connector       connectors.ApiConnector
}

func (e *EthArchiveLabelsDetector) DetectLabels() map[string]string {
	block := e.readEarliestBlock()

	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", []any{"0x0000000000000000000000000000000000000000", block}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect archival state of upstream '%s'", e.upstreamId)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		log.Error().Err(resp.GetError()).Msgf("unable to detect archival state of upstream '%s'", e.upstreamId)
		return map[string]string{
			"archive": "false",
		}
	}
	return map[string]string{
		"archive": "true",
	}
}

func NewEthArchiveLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthArchiveLabelsDetector {
	return &EthArchiveLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
	}
}

func (e *EthArchiveLabelsDetector) readEarliestBlock() string {
	switch e.chain {
	case chains.ARBITRUM:
		return ArbitrumNitroBlock
	case chains.OPTIMISM:
		return OptimismBedrockBlock
	case chains.EVMOS:
		return EvmosGenesisBlock
	default:
		return EarliestBlock
	}
}

var _ LabelsDetector = (*EthArchiveLabelsDetector)(nil)
