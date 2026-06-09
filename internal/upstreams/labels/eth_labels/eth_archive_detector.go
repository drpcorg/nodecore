package eth_labels

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
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
	if !e.hasArchiveState(e.readEarliestBlock()) {
		return map[string]string{"archive": "false"}
	}
	recentBlock, ok := e.recentArchiveProbeBlock()
	if ok && !e.hasArchiveState(recentBlock) {
		return map[string]string{"archive": "false"}
	}
	return map[string]string{"archive": "true"}
}

func (e *EthArchiveLabelsDetector) hasArchiveState(block string) bool {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", []any{"0x0000000000000000000000000000000000000000", block}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect archival state of upstream '%s'", e.upstreamId)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		log.Error().Err(resp.GetError()).Msgf("unable to detect archival state of upstream '%s' at block '%s'", e.upstreamId, block)
		return false
	}
	return true
}

func (e *EthArchiveLabelsDetector) recentArchiveProbeBlock() (string, bool) {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create latest block request for archive detection of upstream '%s'", e.upstreamId)
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		log.Error().Err(resp.GetError()).Msgf("unable to read latest block for archive detection of upstream '%s'", e.upstreamId)
		return "", false
	}
	latestRaw, err := resp.ResponseResultString()
	if err != nil {
		log.Error().Err(err).Msgf("unable to parse latest block for archive detection of upstream '%s'", e.upstreamId)
		return "", false
	}
	latest, err := strconv.ParseUint(strings.TrimPrefix(strings.Trim(strings.TrimSpace(latestRaw), "\""), "0x"), 16, 64)
	if err != nil || latest <= 10000 {
		if err != nil {
			log.Error().Err(err).Msgf("unable to parse latest block for archive detection of upstream '%s'", e.upstreamId)
		}
		return "", false
	}
	return fmt.Sprintf("0x%x", latest-10000), true
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

var _ labels.LabelsDetector = (*EthArchiveLabelsDetector)(nil)
