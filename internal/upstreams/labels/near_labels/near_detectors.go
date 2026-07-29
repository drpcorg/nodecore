package near_labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// neard is the only production NEAR client, so the type is a constant
const nearClientType = "neard"

type NearClientLabelsDetector struct {
	chain chains.Chain
}

func NewNearClientLabelsDetector(chain chains.Chain) *NearClientLabelsDetector {
	return &NearClientLabelsDetector{chain: chain}
}

func (n *NearClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("status", []any{}, n.chain)
}

type nearStatusResponse struct {
	Version struct {
		Version string `json:"version"`
	} `json:"version"`
}

func (n *NearClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var status nearStatusResponse
	if err := sonic.Unmarshal(data, &status); err != nil {
		return "", "", fmt.Errorf("near status payload unparseable: %w", err)
	}
	// version looks like {"version":{"version":"2.13.1","build":"unknown","rustc_version":"1.93.0"},...}
	return status.Version.Version, nearClientType, nil
}

var _ labels.ClientLabelsDetector = (*NearClientLabelsDetector)(nil)
