package stellar_labels

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// horizon is SDF's only implementation of the API, so the type is a constant
const stellarHorizonClientType = "horizon"

type StellarHorizonClientLabelsDetector struct {
	chain chains.Chain
}

func NewStellarHorizonClientLabelsDetector(chain chains.Chain) *StellarHorizonClientLabelsDetector {
	return &StellarHorizonClientLabelsDetector{chain: chain}
}

func (s *StellarHorizonClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET#/", nil, s.chain), nil
}

type stellarHorizonRootResponse struct {
	HorizonVersion string `json:"horizon_version"`
}

func (s *StellarHorizonClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var root stellarHorizonRootResponse
	if err := sonic.Unmarshal(data, &root); err != nil {
		return "", "", fmt.Errorf("horizon root document payload unparseable: %w", err)
	}
	// horizon_version looks like "27.0.0-<commit>";
	// keep the semver prefix, drop the commit suffix
	version, _, _ := strings.Cut(root.HorizonVersion, "-")
	return version, stellarHorizonClientType, nil
}

var _ labels.ClientLabelsDetector = (*StellarHorizonClientLabelsDetector)(nil)
