package stellar_labels

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// stellar-rpc (formerly soroban-rpc) is the only production client, so the type is a constant
const stellarClientType = "stellar-rpc"

type stellarVersionInfo struct {
	Version string `json:"version"`
}

type StellarClientLabelsDetector struct {
	chain chains.Chain
}

func NewStellarClientLabelsDetector(chain chains.Chain) *StellarClientLabelsDetector {
	return &StellarClientLabelsDetector{chain: chain}
}

func (s *StellarClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("getVersionInfo", map[string]any{}, s.chain)
}

func (s *StellarClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var versionInfo stellarVersionInfo
	if err := sonic.Unmarshal(data, &versionInfo); err != nil {
		return "", "", fmt.Errorf("stellar getVersionInfo payload unparseable: %w", err)
	}
	// version reads "27.1.1-<commit>"; keep the semver prefix, drop the commit
	version, _, _ := strings.Cut(versionInfo.Version, "-")
	return version, stellarClientType, nil
}

var _ labels.ClientLabelsDetector = (*StellarClientLabelsDetector)(nil)
