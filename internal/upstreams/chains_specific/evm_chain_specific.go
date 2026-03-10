package specific

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/ethereum/go-ethereum/rpc"
)

type ChainSpecific interface {
	GetLatestBlock(ctx context.Context) (*protocol.Block, error)
	GetFinalizedBlock(context.Context) (*protocol.Block, error)

	ParseBlock([]byte) (*protocol.Block, error)
	ParseSubscriptionBlock(data []byte) (*protocol.Block, error)

	SubscribeHeadRequest() (protocol.RequestHolder, error)
	SettingsValidators() []validations.SettingsValidator
}

type EvmChainSpecificObject struct {
	upstreamId string
	connector  connectors.ApiConnector
	chain      *chains.ConfiguredChain
	options    *config.UpstreamOptions
}

func NewEvmChainSpecific(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *config.UpstreamOptions,
) *EvmChainSpecificObject {
	return &EvmChainSpecificObject{
		upstreamId: upstreamId,
		connector:  connector,
		chain:      chain,
		options:    options,
	}
}

func (e *EvmChainSpecificObject) SettingsValidators() []validations.SettingsValidator {
	settingsValidators := make([]validations.SettingsValidator, 0)

	if !*e.options.DisableChainValidation {
		settingsValidators = append(settingsValidators, validations.NewChainValidator(e.upstreamId, e.connector, e.chain, e.options))
	}

	return settingsValidators
}

var _ ChainSpecific = (*EvmChainSpecificObject)(nil)

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context) (*protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.LatestBlockNumber)
}

func (e *EvmChainSpecificObject) GetFinalizedBlock(ctx context.Context) (*protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.FinalizedBlockNumber)
}

func (e *EvmChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (*protocol.Block, error) {
	return e.ParseBlock(blockBytes)
}

func (e *EvmChainSpecificObject) ParseBlock(blockBytes []byte) (*protocol.Block, error) {
	evmBlock := EvmBlock{}
	err := sonic.Unmarshal(blockBytes, &evmBlock)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the evm block, reason - %s", err.Error())
	}
	if evmBlock.Height == nil {
		return nil, fmt.Errorf("couldn't parse the evm block, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		uint64(evmBlock.Height.Int64()),
		0,
		blockchain.NewHashIdFromString(evmBlock.Hash),
		blockchain.NewHashIdFromString(evmBlock.Parent),
	), nil
}

func (e *EvmChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []interface{}{"newHeads"})
}

func (e *EvmChainSpecificObject) getBlockByTag(ctx context.Context, connector connectors.ApiConnector, blockTag rpc.BlockNumber) (*protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockByNumber", []interface{}{blockTag, false})
	if err != nil {
		return nil, err
	}

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}

	parsedBlock, err := e.ParseBlock(response.ResponseResult())
	if err != nil {
		return nil, err
	}
	return parsedBlock, nil
}

type EvmBlock struct {
	Hash   string           `json:"hash"`
	Parent string           `json:"parentHash"`
	Height *rpc.BlockNumber `json:"number"`
}
