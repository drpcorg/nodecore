package labels

import (
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AztecClientLabelsDetector struct {
}

func (a *AztecClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("node_getNodeVersion", nil, chains.AZTEC_MAINNET)
}

func (a *AztecClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var version string
	if err := sonic.Unmarshal(data, &version); err != nil {
		// node_getNodeVersion returns a JSON string. If it ever returns an object
		// (e.g. node_getNodeInfo-like payload), fall back to extracting nodeVersion.
		var info aztecNodeInfo
		if err2 := sonic.Unmarshal(data, &info); err2 != nil {
			return "", "", err
		}
		version = info.NodeVersion
	}
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	return version, "aztec", nil
}

func NewAztecClientLabelsDetector() *AztecClientLabelsDetector {
	return &AztecClientLabelsDetector{}
}

var _ ClientLabelsDetector = (*AztecClientLabelsDetector)(nil)

type aztecNodeInfo struct {
	NodeVersion string `json:"nodeVersion"`
}
