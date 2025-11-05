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
	"github.com/ethereum/go-ethereum/rpc"
)

type ChainSpecific interface {
	GetLatestBlock(context.Context, connectors.ApiConnector) (*protocol.Block, error)
	GetFinalizedBlock(context.Context, connectors.ApiConnector) (*protocol.Block, error)
	ParseBlock([]byte) (*protocol.Block, error)
	SubscribeHeadRequest() (protocol.RequestHolder, error)
	ParseSubscriptionBlock([]byte) (*protocol.Block, error)
	SettingsValidators(upstreamId string, connector connectors.ApiConnector, chain chains.ConfiguredChain, options *config.UpstreamOptions) []validations.SettingsValidator
}

var EvmChainSpecific *EvmChainSpecificObject

func init() {
	EvmChainSpecific = &EvmChainSpecificObject{}
}

type EvmChainSpecificObject struct {
}

func (e *EvmChainSpecificObject) SettingsValidators(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.ConfiguredChain,
	options *config.UpstreamOptions,
) []validations.SettingsValidator {
	settingsValidators := make([]validations.SettingsValidator, 0)

	if !*options.DisableChainValidation {
		settingsValidators = append(settingsValidators, validations.NewChainValidator(upstreamId, connector, chain, options))
	}

	return settingsValidators
}

var _ ChainSpecific = (*EvmChainSpecificObject)(nil)

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	return e.getBlockByTag(ctx, connector, rpc.LatestBlockNumber)
}

func (e *EvmChainSpecificObject) GetFinalizedBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	return e.getBlockByTag(ctx, connector, rpc.FinalizedBlockNumber)
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

	return protocol.NewBlock(uint64(evmBlock.Height.Int64()), 0, evmBlock.Hash), nil
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
	Height *rpc.BlockNumber `json:"number"`
}
