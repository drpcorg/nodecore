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

type StellarClientLabelsDetector struct {
	chain chains.Chain
}

func NewStellarClientLabelsDetector(chain chains.Chain) *StellarClientLabelsDetector {
	return &StellarClientLabelsDetector{chain: chain}
}

func (s *StellarClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("getVersionInfo", map[string]any{}, s.chain)
}

type stellarVersionInfoResponse struct {
	Version string `json:"version"`
}

func (s *StellarClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var versionInfo stellarVersionInfoResponse
	if err := sonic.Unmarshal(data, &versionInfo); err != nil {
		return "", "", fmt.Errorf("stellar getVersionInfo payload unparseable: %w", err)
	}
	// version looks like {"version":"27.1.1-7e71...","commitHash":"...","captiveCoreVersion":"..."};
	// keep the semver prefix, drop the commit suffix
	version, _, _ := strings.Cut(versionInfo.Version, "-")
	return version, stellarClientType, nil
}

var _ labels.ClientLabelsDetector = (*StellarClientLabelsDetector)(nil)
