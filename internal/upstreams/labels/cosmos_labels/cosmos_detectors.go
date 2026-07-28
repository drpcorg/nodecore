package cosmos_labels

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tendermint_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type CosmosClientLabelsDetector struct {
	chain chains.Chain
}

func NewCosmosClientLabelsDetector(chain chains.Chain) *CosmosClientLabelsDetector {
	return &CosmosClientLabelsDetector{chain: chain}
}

func (c *CosmosClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return specific_helpers.CosmosNodeInfoRequest(c.chain), nil
}

func (c *CosmosClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	nodeInfo, err := specific_helpers.ParseCosmosNodeInfo(data)
	if err != nil {
		return "", "", err
	}
	version := nodeInfo.ApplicationVersion.Version
	if version == "" {
		version = nodeInfo.DefaultNodeInfo.Version
	}
	return version, tendermint_labels.CosmosClient, nil
}

var _ labels.ClientLabelsDetector = (*CosmosClientLabelsDetector)(nil)
