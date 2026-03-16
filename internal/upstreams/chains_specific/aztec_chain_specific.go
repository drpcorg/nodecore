package specific

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
)

type AztecChainSpecificObject struct {
	upstreamId string
	connector  connectors.ApiConnector
}

func (a *AztecChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return nil
}

func (a *AztecChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return nil
}

func (a *AztecChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (a *AztecChainSpecificObject) GetLatestBlock(ctx context.Context) (*protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getBlock", []interface{}{"latest"})
	if err != nil {
		return nil, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AztecChainSpecificObject) GetFinalizedBlock(_ context.Context) (*protocol.Block, error) {
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

	return protocol.NewBlock(height, 0, blockchain.NewHashIdFromString(block.BlockHash), blockchain.EmptyHash), nil
}

func (a *AztecChainSpecificObject) ParseSubscriptionBlock(_ []byte) (*protocol.Block, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

func (a *AztecChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

func NewAztecChainSpecificObject(
	upstreamId string,
	connector connectors.ApiConnector,
) *AztecChainSpecificObject {
	return &AztecChainSpecificObject{
		upstreamId: upstreamId,
		connector:  connector,
	}
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

var _ ChainSpecific = (*AztecChainSpecificObject)(nil)
