package ripple_labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	rippledClientType = "rippled"
	clioClientType    = "clio"
)

type RippleClientLabelsDetector struct {
	chain chains.Chain
}

func NewRippleClientLabelsDetector(chain chains.Chain) *RippleClientLabelsDetector {
	return &RippleClientLabelsDetector{chain: chain}
}

func (r *RippleClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	// rippled expects params as a one-object array - [{}]
	return protocol.NewInternalUpstreamJsonRpcRequest("server_info", []any{map[string]any{}}, r.chain)
}

type rippleServerInfoResponse struct {
	Info struct {
		BuildVersion string `json:"build_version"`
		ClioVersion  string `json:"clio_version"`
	} `json:"info"`
}

func (r *RippleClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var serverInfo rippleServerInfoResponse
	if err := sonic.Unmarshal(data, &serverInfo); err != nil {
		return "", "", fmt.Errorf("ripple server_info payload unparseable: %w", err)
	}
	// clio responds with {"info":{"clio_version":"2.x","rippled":{...}},...},
	// rippled with {"info":{"build_version":"3.2.0",...},"status":"success"}
	if serverInfo.Info.ClioVersion != "" {
		return serverInfo.Info.ClioVersion, clioClientType, nil
	}
	return serverInfo.Info.BuildVersion, rippledClientType, nil
}

var _ labels.ClientLabelsDetector = (*RippleClientLabelsDetector)(nil)
