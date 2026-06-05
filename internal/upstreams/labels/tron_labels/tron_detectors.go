package tron_labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const TronClient = "tron"

type TronClientLabelsDetector struct {
	chain chains.Chain
}

func NewTronClientLabelsDetector(chain chains.Chain) *TronClientLabelsDetector {
	return &TronClientLabelsDetector{chain: chain}
}

func (t *TronClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("POST", "/wallet/getnodeinfo", t.chain), nil
}

type tronNodeInfoResponse struct {
	ConfigNodeInfo struct {
		CodeVersion string `json:"codeVersion"`
	} `json:"configNodeInfo"`
}

func (t *TronClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var info tronNodeInfoResponse
	if err := sonic.Unmarshal(data, &info); err != nil {
		return "", "", fmt.Errorf("tron /wallet/getnodeinfo payload unparseable: %w", err)
	}
	return info.ConfigNodeInfo.CodeVersion, TronClient, nil
}

var _ labels.ClientLabelsDetector = (*TronClientLabelsDetector)(nil)
