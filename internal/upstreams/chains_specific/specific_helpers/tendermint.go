package specific_helpers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type TendermintStatus struct {
	NodeInfo TendermintNodeInfo `json:"node_info"`
	SyncInfo TendermintSyncInfo `json:"sync_info"`
}

type TendermintNodeInfo struct {
	Network string `json:"network"`
	Version string `json:"version"`
	Moniker string `json:"moniker"`
}

type TendermintSyncInfo struct {
	EarliestBlockHeight string `json:"earliest_block_height"`
	CatchingUp          bool   `json:"catching_up"`
}

func TendermintCall(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
	method string,
	params map[string]any,
) ([]byte, error) {
	if params == nil {
		params = map[string]any{}
	}
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

func FetchTendermintStatus(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*TendermintStatus, error) {
	raw, err := TendermintCall(ctx, connector, chain, "status", nil)
	if err != nil {
		return nil, err
	}
	return ParseTendermintStatus(raw)
}

func ParseTendermintStatus(raw []byte) (*TendermintStatus, error) {
	var status TendermintStatus
	if err := sonic.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("tendermint status payload unparseable: %w", err)
	}
	return &status, nil
}

func ParseDecimalHeight(value string) (uint64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty height")
	}
	return strconv.ParseUint(value, 10, 64)
}
