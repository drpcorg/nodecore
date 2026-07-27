package aptos_labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aptos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AptosClientLabelsDetector struct {
	chain chains.Chain
}

func NewAptosClientLabelsDetector(chain chains.Chain) *AptosClientLabelsDetector {
	return &AptosClientLabelsDetector{chain: chain}
}

func (a *AptosClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET#/v1", nil, a.chain), nil
}

func (a *AptosClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var info aptos_validations.AptosLedgerInfo
	if err := sonic.Unmarshal(data, &info); err != nil {
		return "", "", fmt.Errorf("aptos /v1 payload unparseable: %w", err)
	}
	version := info.GitHash
	if len(version) > 12 {
		version = version[:12]
	}
	return version, "aptos-node", nil
}

var _ labels.ClientLabelsDetector = (*AptosClientLabelsDetector)(nil)
