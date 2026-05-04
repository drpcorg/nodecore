package labels

import (
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AztecClientLabelsDetector struct {
	chain chains.Chain
}

func NewAztecClientLabelsDetector(chain chains.Chain) *AztecClientLabelsDetector {
	return &AztecClientLabelsDetector{chain: chain}
}

func (a *AztecClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("node_getNodeVersion", []string{}, a.chain)
}

type aztecNodeInfo struct {
	NodeVersion string `json:"nodeVersion"`
}

func (a *AztecClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var version string
	if err := sonic.Unmarshal(data, &version); err != nil {
		// node_getNodeVersion returns a JSON string. If it ever returns an object
		// (e.g. node_getNodeInfo-like payload), fall back to extracting nodeVersion.
		var info aztecNodeInfo
		if err2 := sonic.Unmarshal(data, &info); err2 != nil {
			// Surface the object-decode failure (the string-decode error above
			// only triggered the fallback - the actual reason we couldn't
			// extract a version is err2).
			return "", "", err2
		}
		version = info.NodeVersion
	}
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	return version, "aztec", nil
}

var _ ClientLabelsDetector = (*AztecClientLabelsDetector)(nil)
