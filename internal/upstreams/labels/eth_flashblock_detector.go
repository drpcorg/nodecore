package labels

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type EthFlashBlockDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (e *EthFlashBlockDetector) DetectLabels() map[string]string {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockByNumber", []any{"pending", false}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect flashblocks of upstream '%s'", e.upstreamId)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		log.Error().Err(resp.GetError()).Msgf("unable to detect flashblocks of upstream '%s'", e.upstreamId)
		return nil
	}

	root, err := sonic.Get(resp.ResponseResult())
	if err != nil {
		log.Error().Err(err).Msgf("unable parse flashblocks response of upstream '%s'", e.upstreamId)
		return nil
	}

	value := "false"
	if stateRoot, _ := root.Get("stateRoot").String(); stateRoot == "0x0000000000000000000000000000000000000000000000000000000000000000" {
		value = "true"
	}
	return map[string]string{"flashblocks": value}
}

func NewEthFlashBlockDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthFlashBlockDetector {
	return &EthFlashBlockDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

var _ LabelsDetector = (*EthFlashBlockDetector)(nil)
