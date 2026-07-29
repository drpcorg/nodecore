package beacon_labels

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// BeaconChainClientLabelsDetector reads the consensus client version from the
// standard beacon REST endpoint GET /eth/v1/node/version. The version string
// looks like "Lighthouse/v5.1.3-..." or "Prysm/v5.0.0/..."; the leading token
// before the first '/' is treated as the client type.
type BeaconChainClientLabelsDetector struct {
	chain chains.Chain
}

func NewBeaconChainClientLabelsDetector(chain chains.Chain) *BeaconChainClientLabelsDetector {
	return &BeaconChainClientLabelsDetector{chain: chain}
}

func (b *BeaconChainClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/node/version", nil, b.chain), nil
}

type beaconVersionResponse struct {
	Data beaconVersionData `json:"data"`
}

type beaconVersionData struct {
	Version string `json:"version"`
}

func (b *BeaconChainClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var versions beaconVersionResponse
	if err := sonic.Unmarshal(data, &versions); err != nil {
		return "", "", fmt.Errorf("beacon /eth/v1/node/version payload unparseable: %w", err)
	}
	version := strings.TrimSpace(versions.Data.Version)
	if version == "" {
		return "", "", nil
	}
	clientType := version
	if idx := strings.Index(version, "/"); idx > 0 {
		clientType = version[:idx]
	}
	return version, strings.ToLower(clientType), nil
}

var _ labels.ClientLabelsDetector = (*BeaconChainClientLabelsDetector)(nil)
