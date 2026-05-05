package labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AlgorandClientLabelsDetector struct {
	chain chains.Chain
}

func NewAlgorandClientLabelsDetector(chain chains.Chain) *AlgorandClientLabelsDetector {
	return &AlgorandClientLabelsDetector{chain: chain}
}

func (a *AlgorandClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET", "/versions", a.chain), nil
}

type algorandVersionsResponse struct {
	Build algorandBuildInfo `json:"build"`
}

type algorandBuildInfo struct {
	Major       int `json:"major"`
	Minor       int `json:"minor"`
	BuildNumber int `json:"build_number"`
}

func (a *AlgorandClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var versions algorandVersionsResponse
	if err := sonic.Unmarshal(data, &versions); err != nil {
		return "", "", fmt.Errorf("algorand /versions payload unparseable: %w", err)
	}
	build := versions.Build
	if build.Major == 0 && build.Minor == 0 && build.BuildNumber == 0 {
		return "", "algod", nil
	}
	return fmt.Sprintf("%d.%d.%d", build.Major, build.Minor, build.BuildNumber), "algod", nil
}

var _ ClientLabelsDetector = (*AlgorandClientLabelsDetector)(nil)
