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
	tonIndexGoTitle = "TON Index (Go)"

	tonIndexGoClientType = "ton-index-go"
	tonIndexClientType   = "ton-index"
)

type TonV3ClientLabelsDetector struct {
	chain chains.Chain
}

func NewTonV3ClientLabelsDetector(chain chains.Chain) *TonV3ClientLabelsDetector {
	return &TonV3ClientLabelsDetector{chain: chain}
}

func (t *TonV3ClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamRestRequest("GET#/doc.json", nil, t.chain), nil
}

type tonDocJsonResponse struct {
	Info struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
}

func (t *TonV3ClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var doc tonDocJsonResponse
	if err := sonic.Unmarshal(data, &doc); err != nil {
		return "", "", fmt.Errorf("ton /doc.json payload unparseable: %w", err)
	}
	// info looks like {"info":{"title":"TON Index (Go)","version":"1.2.8"},...}
	clientType := tonIndexClientType
	if doc.Info.Title == tonIndexGoTitle {
		clientType = tonIndexGoClientType
	}
	return strings.TrimPrefix(doc.Info.Version, "v"), clientType, nil
}

var _ labels.ClientLabelsDetector = (*TonV3ClientLabelsDetector)(nil)
