package specific

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/samber/lo"
)

var SolanaChainSpecific *SolanaChainSpecificObject

func init() {
	SolanaChainSpecific = &SolanaChainSpecificObject{}
}

type SolanaChainSpecificObject struct {
}

var _ ChainSpecific = (*SolanaChainSpecificObject)(nil)

func (s *SolanaChainSpecificObject) GetLatestBlock(ctx context.Context, connector connectors.ApiConnector) (*protocol.Block, error) {
	slot, err := getLatestSlot(ctx, connector)
	if err != nil {
		return nil, err
	}

	maxBlock, err := getMaxBlock(ctx, connector, slot)
	if err != nil {
		return nil, err
	}

	block, err := getBlock(ctx, connector, maxBlock)
	if err != nil {
		return nil, err
	}

	parsedBlock, err := s.ParseBlock(block)
	if err != nil {
		return nil, err
	}
	parsedBlock.BlockData.Slot = maxBlock
	return parsedBlock, nil
}

func (s *SolanaChainSpecificObject) ParseBlock(blockBytes []byte) (*protocol.Block, error) {
	solanaBlock := SolanaBlock{}
	err := sonic.Unmarshal(blockBytes, &solanaBlock)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the solana block, reason - %s", err.Error())
	}

	return protocol.NewBlock(solanaBlock.Height, 0, solanaBlock.Hash, blockBytes), nil
}

func (s *SolanaChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (*protocol.Block, error) {
	solanaSubBlock := SolanaSubscriptionBlock{}
	err := sonic.Unmarshal(blockBytes, &solanaSubBlock)
	if err != nil {
		return nil, err
	}

	return protocol.NewBlock(
		solanaSubBlock.Value.Block.Height,
		solanaSubBlock.Context.Slot,
		solanaSubBlock.Value.Block.Hash,
		blockBytes,
	), nil
}

func (s *SolanaChainSpecificObject) SubscribeHeadRequest() (protocol.UpstreamRequest, error) {
	params := map[string]interface{}{
		"showRewards":        false,
		"transactionDetails": "none",
	}
	return protocol.NewJsonRpcUpstreamRequest(1, "blockSubscribe", []interface{}{"all", params}, false)
}

func getLatestSlot(ctx context.Context, connector connectors.ApiConnector) (uint64, error) {
	slotReq, err := protocol.NewJsonRpcUpstreamRequest(1, "getSlot", nil, false)
	if err != nil {
		return 0, err
	}
	slotResponse := connector.SendRequest(ctx, slotReq)
	if slotResponse.HasError() {
		return 0, slotResponse.ResponseError()
	}

	slot := protocol.ResultAsNumber(slotResponse.ResponseResult())

	return slot, nil
}

func getMaxBlock(ctx context.Context, connector connectors.ApiConnector, slot uint64) (uint64, error) {
	blocksReq, err := protocol.NewJsonRpcUpstreamRequest(1, "getBlocks", []interface{}{slot - 10, slot}, false)
	if err != nil {
		return 0, err
	}
	blocksResponse := connector.SendRequest(ctx, blocksReq)
	if blocksResponse.HasError() {
		return 0, blocksResponse.ResponseError()
	}

	var blocks []uint64
	err = sonic.Unmarshal(blocksResponse.ResponseResult(), &blocks)
	if err != nil {
		return 0, err
	}

	return lo.Max(blocks), nil
}

func getBlock(ctx context.Context, connector connectors.ApiConnector, block uint64) ([]byte, error) {
	params := map[string]interface{}{
		"showRewards":                    false,
		"transactionDetails":             "none",
		"maxSupportedTransactionVersion": 0,
	}
	blockReq, err := protocol.NewJsonRpcUpstreamRequest(1, "getBlock", []interface{}{block, params}, false)
	if err != nil {
		return nil, err
	}
	blockResponse := connector.SendRequest(ctx, blockReq)
	if blockResponse.HasError() {
		return nil, blockResponse.ResponseError()
	}

	return blockResponse.ResponseResult(), nil
}

type SolanaBlock struct {
	Height uint64 `json:"blockHeight"`
	Hash   string `json:"blockhash"`
}

type SolanaSubscriptionBlock struct {
	Context Context `json:"context"`
	Value   Value   `json:"value"`
}

type Context struct {
	Slot uint64 `json:"slot"`
}

type Value struct {
	Block SolanaBlock `json:"block"`
}
