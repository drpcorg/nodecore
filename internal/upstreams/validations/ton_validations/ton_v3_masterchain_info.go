package ton_validations

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// TonV3Block is a block header as the toncenter v3 indexer serves it.
// gen_utime is a STRING of unix seconds in v3 (unlike v2's number).
type TonV3Block struct {
	Seqno    uint64 `json:"seqno"`
	RootHash string `json:"root_hash"`
	GenUtime string `json:"gen_utime"`
	GlobalId int64  `json:"global_id"`
}

// TonV3MasterchainInfo is the BARE (no ok-envelope) response of
// GET /api/v3/masterchainInfo; first directly advertises the indexer's
// history floor.
type TonV3MasterchainInfo struct {
	Last  TonV3Block `json:"last"`
	First TonV3Block `json:"first"`
}

func FetchV3MasterchainInfo(
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) (*TonV3MasterchainInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/api/v3/masterchainInfo", nil, chain)

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var info TonV3MasterchainInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return nil, err
	}
	return &info, nil
}
