package specific_helpers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	CosmosLatestBlockRoute   = "GET#/cosmos/base/tendermint/v1beta1/blocks/latest"
	CosmosBlockByHeightRoute = "GET#/cosmos/base/tendermint/v1beta1/blocks/*"
	CosmosNodeInfoRoute      = "GET#/cosmos/base/tendermint/v1beta1/node_info"
	CosmosSyncingRoute       = "GET#/cosmos/base/tendermint/v1beta1/syncing"
)

type CosmosNodeInfo struct {
	DefaultNodeInfo    CosmosDefaultNodeInfo    `json:"default_node_info"`
	ApplicationVersion CosmosApplicationVersion `json:"application_version"`
}

type CosmosDefaultNodeInfo struct {
	Network string `json:"network"`
	Version string `json:"version"`
	Moniker string `json:"moniker"`
}

type CosmosApplicationVersion struct {
	Name             string `json:"name"`
	AppName          string `json:"app_name"`
	Version          string `json:"version"`
	CosmosSdkVersion string `json:"cosmos_sdk_version"`
}

type CosmosBlockResult struct {
	BlockId CosmosBlockId   `json:"block_id"`
	Block   CosmosBlockData `json:"block"`
}

type CosmosBlockId struct {
	Hash string `json:"hash"`
}

type CosmosBlockData struct {
	Header CosmosHeader `json:"header"`
}

type CosmosHeader struct {
	Height      string        `json:"height"`
	Time        string        `json:"time"`
	LastBlockId CosmosBlockId `json:"last_block_id"`
}

func CosmosNodeInfoRequest(chain chains.Chain) protocol.RequestHolder {
	return protocol.NewInternalUpstreamRestRequest(CosmosNodeInfoRoute, nil, chain)
}

func CosmosBlockByHeightRequest(chain chains.Chain, height int64) protocol.RequestHolder {
	return protocol.NewInternalUpstreamRestRequest(
		CosmosBlockByHeightRoute,
		&protocol.RequestParams{PathParams: []string{strconv.FormatInt(height, 10)}},
		chain,
	)
}

func FetchCosmosNodeInfo(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*CosmosNodeInfo, error) {
	response := connector.SendRequest(ctx, CosmosNodeInfoRequest(chain))
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParseCosmosNodeInfo(response.ResponseResult())
}

func ParseCosmosNodeInfo(raw []byte) (*CosmosNodeInfo, error) {
	var nodeInfo CosmosNodeInfo
	if err := sonic.Unmarshal(raw, &nodeInfo); err != nil {
		return nil, fmt.Errorf("cosmos node_info payload unparseable: %w", err)
	}
	return &nodeInfo, nil
}

func FetchCosmosLatestBlock(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) ([]byte, error) {
	request := protocol.NewInternalUpstreamRestRequest(CosmosLatestBlockRoute, nil, chain)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

func ParseCosmosBlock(raw []byte) (*CosmosBlockResult, error) {
	var result CosmosBlockResult
	if err := sonic.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("cosmos block payload unparseable: %w", err)
	}
	return &result, nil
}

func FetchCosmosSyncing(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (bool, error) {
	request := protocol.NewInternalUpstreamRestRequest(CosmosSyncingRoute, nil, chain)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return false, response.GetError()
	}
	var parsed struct {
		Syncing bool `json:"syncing"`
	}
	if err := sonic.Unmarshal(response.ResponseResult(), &parsed); err != nil {
		return false, fmt.Errorf("cosmos syncing payload unparseable: %w", err)
	}
	return parsed.Syncing, nil
}
