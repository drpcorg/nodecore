package specific_helpers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	tronapi "github.com/drpcorg/public/pkg/tron/api"
	troncore "github.com/drpcorg/public/pkg/tron/core"
	"google.golang.org/protobuf/proto"
)

// TronGrpcNowBlockRequest builds the latest-block probe: GetBlock with no
// id_or_num ("latest") and detail false, the gRPC twin of REST's
// POST /wallet/getblock {"detail": false}. The reply carries the block id and
// the header only - GetNowBlock2 would ship every transaction of the block on
// each poll.
func TronGrpcNowBlockRequest(chain chains.Chain) (protocol.RequestHolder, error) {
	return tronGrpcBlockRequest(chain, "")
}

// FetchTronGrpcNowBlock calls the latest-block probe and returns the raw
// BlockExtention bytes - probes cross the schema boundary as bytes, ParseBlock
// decodes them.
func FetchTronGrpcNowBlock(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) ([]byte, error) {
	request, err := TronGrpcNowBlockRequest(chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

// ParseTronGrpcBlock unmarshals a BlockExtention. Zero bytes are a valid
// serialization (java-tron's "no such block"), so emptiness alone is not an
// error - callers validate the fields they need.
func ParseTronGrpcBlock(raw []byte) (*tronapi.BlockExtention, error) {
	var block tronapi.BlockExtention
	if err := proto.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("tron grpc block payload unparseable: %w", err)
	}
	return &block, nil
}

// TronGrpcBlockByNumRequest builds the header-only probe for one height:
// GetBlock with id_or_num set and detail false, the twin of REST's
// POST /wallet/getblock {"id_or_num": "<n>", "detail": false}.
func TronGrpcBlockByNumRequest(chain chains.Chain, height int64) (protocol.RequestHolder, error) {
	return tronGrpcBlockRequest(chain, strconv.FormatInt(height, 10))
}

// TronGrpcNodeInfoRequest builds the GetNodeInfo probe request.
func TronGrpcNodeInfoRequest(chain chains.Chain) protocol.RequestHolder {
	return protocol.NewInternalUpstreamGrpcRequest("/protocol.Wallet/GetNodeInfo", nil, chain)
}

// FetchTronGrpcNodeInfo calls GetNodeInfo and returns the typed reply. The
// caller owns the timeout.
func FetchTronGrpcNodeInfo(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*troncore.NodeInfo, error) {
	response := connector.SendRequest(ctx, TronGrpcNodeInfoRequest(chain))
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParseTronGrpcNodeInfo(response.ResponseResult())
}

// ParseTronGrpcNodeInfo unmarshals a NodeInfo.
func ParseTronGrpcNodeInfo(raw []byte) (*troncore.NodeInfo, error) {
	var nodeInfo troncore.NodeInfo
	if err := proto.Unmarshal(raw, &nodeInfo); err != nil {
		return nil, fmt.Errorf("tron grpc node info payload unparseable: %w", err)
	}
	return &nodeInfo, nil
}

func tronGrpcBlockRequest(chain chains.Chain, idOrNum string) (protocol.RequestHolder, error) {
	body, err := proto.Marshal(&tronapi.BlockReq{IdOrNum: idOrNum, Detail: false})
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal the tron grpc block request: %w", err)
	}
	return protocol.NewInternalUpstreamGrpcRequest("/protocol.Wallet/GetBlock", body, chain), nil
}
