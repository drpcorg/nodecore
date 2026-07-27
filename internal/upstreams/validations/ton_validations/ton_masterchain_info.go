package ton_validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

var errTonNotOk = errors.New("ton node returned ok=false for getMasterchainInfo")

// TonMasterchainInfo is the toncenter v2 response envelope of
// GET /getMasterchainInfo; result.init is the network zerostate.
type TonMasterchainInfo struct {
	Ok     bool `json:"ok"`
	Result struct {
		Last struct {
			Seqno    uint64 `json:"seqno"`
			RootHash string `json:"root_hash"`
		} `json:"last"`
		Init struct {
			RootHash string `json:"root_hash"`
			FileHash string `json:"file_hash"`
		} `json:"init"`
	} `json:"result"`
}

func fetchMasterchainInfo(
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) (*TonMasterchainInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/getMasterchainInfo", nil, chain)

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var info TonMasterchainInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return nil, err
	}
	if !info.Ok {
		return nil, errTonNotOk
	}
	return &info, nil
}
