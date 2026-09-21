package tendermint_labels

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const CosmosClient = "cosmos"

type TendermintClientLabelsDetector struct {
	chain chains.Chain
}

func NewTendermintClientLabelsDetector(chain chains.Chain) *TendermintClientLabelsDetector {
	return &TendermintClientLabelsDetector{chain: chain}
}

func (t *TendermintClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("status", map[string]any{}, t.chain)
}

func (t *TendermintClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	status, err := specific_helpers.ParseTendermintStatus(data)
	if err != nil {
		return "", "", err
	}
	return status.NodeInfo.Version, CosmosClient, nil
}

var _ labels.ClientLabelsDetector = (*TendermintClientLabelsDetector)(nil)
