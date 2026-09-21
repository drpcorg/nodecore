package specific_helpers

import (
	"context"
	"fmt"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"cosmossdk.io/api/tendermint/types"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"google.golang.org/protobuf/proto"
)

// FetchCosmosGrpcLatestBlock calls GetLatestBlock and returns the raw
// response bytes. GetLatestBlockRequest is an empty message, so the body is
// zero bytes on the wire - probes cross the schema boundary as bytes.
func FetchCosmosGrpcLatestBlock(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) ([]byte, error) {
	request := protocol.NewInternalUpstreamGrpcRequest(
		"/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock", nil, chain,
	)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

// ParseCosmosGrpcBlock unmarshals a GetLatestBlockResponse. Zero bytes are a
// valid serialization of the message, so emptiness alone is not an error -
// callers validate the fields they need.
func ParseCosmosGrpcBlock(raw []byte) (*tendermintv1beta1.GetLatestBlockResponse, error) {
	var result tendermintv1beta1.GetLatestBlockResponse
	if err := proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("cosmos grpc block payload unparseable: %w", err)
	}
	return &result, nil
}

// CosmosGrpcBlockHeader extracts the height and the parent block id from a
// block reply, preferring `sdk_block` and falling back to the deprecated
// comet `block` that pre-0.47 SDK nodes are the only ones to fill.
func CosmosGrpcBlockHeader(resp cosmosGrpcBlockCarrier) (int64, []byte) {
	if sdkBlock := resp.GetSdkBlock(); sdkBlock != nil {
		header := sdkBlock.GetHeader()
		return header.GetHeight(), header.GetLastBlockId().GetHash()
	}
	header := resp.GetBlock().GetHeader() //nolint:staticcheck // the fallback for pre-0.47 nodes IS the deprecated field
	return header.GetHeight(), header.GetLastBlockId().GetHash()
}

// cosmosGrpcBlockCarrier abstracts GetLatestBlockResponse and
// GetBlockByHeightResponse, which carry the same block pair.
type cosmosGrpcBlockCarrier interface {
	GetSdkBlock() *tendermintv1beta1.Block
	GetBlock() *types.Block
}

// CosmosGrpcBlockByHeightRequest builds the internal GetBlockByHeight probe
// request.
func CosmosGrpcBlockByHeightRequest(chain chains.Chain, height int64) (protocol.RequestHolder, error) {
	body, err := proto.Marshal(&tendermintv1beta1.GetBlockByHeightRequest{Height: height})
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal the cosmos grpc block-by-height request: %w", err)
	}
	return protocol.NewInternalUpstreamGrpcRequest(
		"/cosmos.base.tendermint.v1beta1.Service/GetBlockByHeight", body, chain,
	), nil
}

// ParseCosmosGrpcBlockByHeight unmarshals a GetBlockByHeightResponse.
func ParseCosmosGrpcBlockByHeight(raw []byte) (*tendermintv1beta1.GetBlockByHeightResponse, error) {
	var result tendermintv1beta1.GetBlockByHeightResponse
	if err := proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("cosmos grpc block payload unparseable: %w", err)
	}
	return &result, nil
}

// CosmosGrpcNodeInfoRequest builds the internal GetNodeInfo probe request.
func CosmosGrpcNodeInfoRequest(chain chains.Chain) protocol.RequestHolder {
	return protocol.NewInternalUpstreamGrpcRequest(
		"/cosmos.base.tendermint.v1beta1.Service/GetNodeInfo", nil, chain,
	)
}

// FetchCosmosGrpcNodeInfo calls GetNodeInfo and returns the typed response.
// The caller owns the timeout.
func FetchCosmosGrpcNodeInfo(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*tendermintv1beta1.GetNodeInfoResponse, error) {
	response := connector.SendRequest(ctx, CosmosGrpcNodeInfoRequest(chain))
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParseCosmosGrpcNodeInfo(response.ResponseResult())
}

// ParseCosmosGrpcNodeInfo unmarshals a GetNodeInfoResponse.
func ParseCosmosGrpcNodeInfo(raw []byte) (*tendermintv1beta1.GetNodeInfoResponse, error) {
	var nodeInfo tendermintv1beta1.GetNodeInfoResponse
	if err := proto.Unmarshal(raw, &nodeInfo); err != nil {
		return nil, fmt.Errorf("cosmos grpc node info payload unparseable: %w", err)
	}
	return &nodeInfo, nil
}

// FetchCosmosGrpcSyncing calls GetSyncing and reports the node's syncing state.
func FetchCosmosGrpcSyncing(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (bool, error) {
	request := protocol.NewInternalUpstreamGrpcRequest(
		"/cosmos.base.tendermint.v1beta1.Service/GetSyncing", nil, chain,
	)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return false, response.GetError()
	}
	var parsed tendermintv1beta1.GetSyncingResponse
	if err := proto.Unmarshal(response.ResponseResult(), &parsed); err != nil {
		return false, fmt.Errorf("cosmos grpc syncing payload unparseable: %w", err)
	}
	return parsed.GetSyncing(), nil
}
