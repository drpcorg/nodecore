package ton_labels

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	tonHttpApiCppTitle = "TON HTTP API C++"

	tonHttpApiCppClientType = "ton-http-api-cpp"
	tonHttpApiClientType    = "ton-http-api"
)

type TonClientLabelsDetector struct {
	chain chains.Chain
}

func NewTonClientLabelsDetector(chain chains.Chain) *TonClientLabelsDetector {
	return &TonClientLabelsDetector{chain: chain}
}

func (t *TonClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET#/openapi.json", nil, t.chain), nil
}

type tonOpenApiResponse struct {
	Info struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
}

func (t *TonClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var openApi tonOpenApiResponse
	if err := sonic.Unmarshal(data, &openApi); err != nil {
		return "", "", fmt.Errorf("ton /openapi.json payload unparseable: %w", err)
	}
	// info looks like {"info":{"title":"TON HTTP API C++","version":"v2.1.13-9bae630"},...}
	clientType := tonHttpApiClientType
	if openApi.Info.Title == tonHttpApiCppTitle {
		clientType = tonHttpApiCppClientType
	}
	return strings.TrimPrefix(openApi.Info.Version, "v"), clientType, nil
}

var _ labels.ClientLabelsDetector = (*TonClientLabelsDetector)(nil)
