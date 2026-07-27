package bitcoin_labels

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type BitcoinClientLabelsDetector struct {
	chain chains.Chain
}

func NewBitcoinClientLabelsDetector(chain chains.Chain) *BitcoinClientLabelsDetector {
	return &BitcoinClientLabelsDetector{chain: chain}
}

func (b *BitcoinClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("getnetworkinfo", []any{}, b.chain)
}

type bitcoinNetworkInfoResponse struct {
	Subversion string `json:"subversion"`
}

func (b *BitcoinClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	var networkInfo bitcoinNetworkInfoResponse
	if err := sonic.Unmarshal(data, &networkInfo); err != nil {
		return "", "", fmt.Errorf("bitcoin getnetworkinfo payload unparseable: %w", err)
	}
	// subversion looks like "/Satoshi:26.1.0/" (Dogecoin Core reports "/Shibetoshi:1.14.9/")
	clientType, version, found := strings.Cut(strings.Trim(networkInfo.Subversion, "/"), ":")
	if !found || clientType == "" || version == "" {
		return "", "bitcoind", nil
	}
	return version, strings.ToLower(clientType), nil
}

var _ labels.ClientLabelsDetector = (*BitcoinClientLabelsDetector)(nil)
