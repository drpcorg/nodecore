package labels

import (
	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type SolanaClientLabelsDetector struct {
}

func (s *SolanaClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
}

func (s *SolanaClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	solanaCore := SolanaVersion{}
	err := sonic.Unmarshal(data, &solanaCore)
	if err != nil {
		return "", "", err
	}
	return solanaCore.SolanaCore, "solana", nil
}

func NewSolanaClientLabelsDetector() *SolanaClientLabelsDetector {
	return &SolanaClientLabelsDetector{}
}

var _ ClientLabelsDetector = (*SolanaClientLabelsDetector)(nil)

type SolanaVersion struct {
	SolanaCore string `json:"solana-core"`
}
