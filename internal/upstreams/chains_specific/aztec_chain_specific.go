package specific

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

var AztecChainSpecific *AztecChainSpecificObject

func init() {
	AztecChainSpecific = &AztecChainSpecificObject{}
}

type AztecChainSpecificObject struct {
}

var _ ChainSpecific = (*AztecChainSpecificObject)(nil)

func (a *AztecChainSpecificObject) SettingsValidators(
	_ string,
	_ connectors.ApiConnector,
	_ chains.ConfiguredChain,
	_ *config.UpstreamOptions,
) []validations.SettingsValidator {
	return nil
}

func (a *AztecChainSpecificObject) GetLatestBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getBlock", []interface{}{"latest"})
	if err != nil {
		return nil, err
	}

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AztecChainSpecificObject) GetFinalizedBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	return nil, nil
}

func (a *AztecChainSpecificObject) ParseBlock(blockBytes []byte) (*protocol.Block, error) {
	block := AztecBlock{}
	err := sonic.Unmarshal(blockBytes, &block)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the aztec block, reason - %s", err.Error())
	}

	height := block.Header.GlobalVariables.BlockNumber
	if height == 0 {
		return nil, fmt.Errorf("couldn't parse the aztec block, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(height, 0, block.BlockHash), nil
}

func (a *AztecChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (*protocol.Block, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

func (a *AztecChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

type AztecBlock struct {
	BlockHash string      `json:"blockHash"`
	Header    AztecHeader `json:"header"`
}

type AztecHeader struct {
	GlobalVariables AztecGlobalVariables `json:"globalVariables"`
}

type AztecGlobalVariables struct {
	BlockNumber uint64 `json:"blockNumber"`
}
