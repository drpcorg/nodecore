package sui_labels

import (
	"fmt"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// SuiClientLabelsDetector parses ONLY the server(8) field of
// GetServiceInfoResponse: "sui-node/1.78.0-03113679fb97" splits on the first
// "/" into the client type and the version; the version drops its -<commit>
// suffix, the same trim stellar's detectors apply.
type SuiClientLabelsDetector struct {
	chain chains.Chain
}

func NewSuiClientLabelsDetector(chain chains.Chain) *SuiClientLabelsDetector {
	return &SuiClientLabelsDetector{chain: chain}
}

func (s *SuiClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return specific_helpers.NewSuiServiceInfoRequest(s.chain), nil
}

func (s *SuiClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	serviceInfo, err := specific_helpers.ParseSuiServiceInfo(data)
	if err != nil {
		return "", "", fmt.Errorf("sui GetServiceInfo payload unparseable: %w", err)
	}
	server := serviceInfo.GetServer()
	if server == "" {
		return "", "", nil
	}
	clientType, fullVersion, found := strings.Cut(server, "/")
	if !found {
		// a bare software name with no version part
		return "", clientType, nil
	}
	version, _, _ := strings.Cut(fullVersion, "-")
	return version, clientType, nil
}

var _ labels.ClientLabelsDetector = (*SuiClientLabelsDetector)(nil)
