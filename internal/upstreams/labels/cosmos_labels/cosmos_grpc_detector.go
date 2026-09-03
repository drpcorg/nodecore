package cosmos_labels

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tendermint_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// CosmosGrpcClientLabelsDetector is the gRPC twin of
// CosmosClientLabelsDetector - the same labels read from
// cosmos.base.tendermint.v1beta1.Service/GetNodeInfo.
type CosmosGrpcClientLabelsDetector struct {
	chain chains.Chain
}

func NewCosmosGrpcClientLabelsDetector(chain chains.Chain) *CosmosGrpcClientLabelsDetector {
	return &CosmosGrpcClientLabelsDetector{chain: chain}
}

func (c *CosmosGrpcClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return specific_helpers.CosmosGrpcNodeInfoRequest(c.chain), nil
}

func (c *CosmosGrpcClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	nodeInfo, err := specific_helpers.ParseCosmosGrpcNodeInfo(data)
	if err != nil {
		return "", "", err
	}
	version := nodeInfo.GetApplicationVersion().GetVersion()
	if version == "" {
		version = nodeInfo.GetDefaultNodeInfo().GetVersion()
	}
	return version, tendermint_labels.CosmosClient, nil
}

var _ labels.ClientLabelsDetector = (*CosmosGrpcClientLabelsDetector)(nil)
