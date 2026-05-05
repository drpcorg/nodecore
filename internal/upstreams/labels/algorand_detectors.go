package labels

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// AlgorandClientLabelsDetector reads node identity from the algod
// `GET /v2/versions` REST endpoint. algod is the only public algorand node
// implementation, so client_type is fixed to `algod` while client_version is
// reconstructed from the build object that algorand publishes in their
// release notes.
type AlgorandClientLabelsDetector struct {
	chain chains.Chain
}

func NewAlgorandClientLabelsDetector(chain chains.Chain) *AlgorandClientLabelsDetector {
	return &AlgorandClientLabelsDetector{chain: chain}
}

func (a *AlgorandClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET", "/v2/versions", a.chain), nil
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
		return "", "", fmt.Errorf("algorand /v2/versions payload unparseable: %w", err)
	}
	build := versions.Build
	if build.Major == 0 && build.Minor == 0 && build.BuildNumber == 0 {
		// Treat an all-zero build object as a missing version - reporting "0.0.0"
		// would be misleading. Empty client_version is preferred over noise.
		return "", "algod", nil
	}
	return fmt.Sprintf("%d.%d.%d", build.Major, build.Minor, build.BuildNumber), "algod", nil
}

var _ ClientLabelsDetector = (*AlgorandClientLabelsDetector)(nil)
