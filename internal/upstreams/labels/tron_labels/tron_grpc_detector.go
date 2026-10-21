package tron_labels

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// TronGrpcClientLabelsDetector is the gRPC twin of TronClientLabelsDetector -
// the same labels read from protocol.Wallet/GetNodeInfo.
type TronGrpcClientLabelsDetector struct {
	chain chains.Chain
}

func NewTronGrpcClientLabelsDetector(chain chains.Chain) *TronGrpcClientLabelsDetector {
	return &TronGrpcClientLabelsDetector{chain: chain}
}

func (t *TronGrpcClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return specific_helpers.TronGrpcNodeInfoRequest(t.chain), nil
}

func (t *TronGrpcClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	nodeInfo, err := specific_helpers.ParseTronGrpcNodeInfo(data)
	if err != nil {
		return "", "", err
	}
	return nodeInfo.GetConfigNodeInfo().GetCodeVersion(), TronClient, nil
}

var _ labels.ClientLabelsDetector = (*TronGrpcClientLabelsDetector)(nil)
