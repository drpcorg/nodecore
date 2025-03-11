package specific

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dshaltie/src/protocol"
	"github.com/drpcorg/dshaltie/src/upstreams/connectors"
	"github.com/ethereum/go-ethereum/rpc"
)

var EvmChainSpecific *EvmChainSpecificObject

func init() {
	EvmChainSpecific = &EvmChainSpecificObject{}
}

type EvmChainSpecificObject struct {
}

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	request, err := protocol.NewJsonRpcUpstreamRequest(1, "eth_getBlockByNumber", []interface{}{"latest", false}, false)
	if err != nil {
		return nil, err
	}

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.ResponseError()
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

	return &protocol.Block{
		Hash:      evmBlock.Hash,
		Height:    uint64(evmBlock.Height.Int64()),
		BlockJson: blockBytes,
	}, nil
}

func (e *EvmChainSpecificObject) SubscribeHeadRequest() (protocol.UpstreamRequest, error) {
	return protocol.NewJsonRpcUpstreamRequest(1, "eth_subscribe", []interface{}{"newHeads"}, false)
}

type EvmBlock struct {
	Hash   string           `json:"hash"`
	Height *rpc.BlockNumber `json:"number"`
}
