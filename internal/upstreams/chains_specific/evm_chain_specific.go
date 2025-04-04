package specific

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/ethereum/go-ethereum/rpc"
)

type ChainSpecific interface {
	GetLatestBlock(context.Context, connectors.ApiConnector) (*protocol.Block, error)
	ParseBlock([]byte) (*protocol.Block, error)
	SubscribeHeadRequest() (protocol.RequestHolder, error)
	ParseSubscriptionBlock([]byte) (*protocol.Block, error)
}

var EvmChainSpecific *EvmChainSpecificObject

func init() {
	EvmChainSpecific = &EvmChainSpecificObject{}
}

type EvmChainSpecificObject struct {
}

var _ ChainSpecific = (*EvmChainSpecificObject)(nil)

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	request, err := protocol.NewJsonRpcUpstreamRequest("1", "eth_getBlockByNumber", []interface{}{"latest", false}, false)
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

func (e *EvmChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (*protocol.Block, error) {
	return e.ParseBlock(blockBytes)
}

func (e *EvmChainSpecificObject) ParseBlock(blockBytes []byte) (*protocol.Block, error) {
	evmBlock := EvmBlock{}
	err := sonic.Unmarshal(blockBytes, &evmBlock)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the evm block, reason - %s", err.Error())
	}

	return protocol.NewBlock(uint64(evmBlock.Height.Int64()), 0, evmBlock.Hash, blockBytes), nil
}

func (e *EvmChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewJsonRpcUpstreamRequest("1", "eth_subscribe", []interface{}{"newHeads"}, false)
}

type EvmBlock struct {
	Hash   string           `json:"hash"`
	Height *rpc.BlockNumber `json:"number"`
}
