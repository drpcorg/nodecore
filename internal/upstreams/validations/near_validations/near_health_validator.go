package near_validations

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// nearStatusClient holds what a `status` call needs; validators embed it and
// call fetchNearStatus without passing params around.
type nearStatusClient struct {
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func (n *nearStatusClient) fetchNearStatus() (*NearStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), n.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("status", []any{}, n.chain.Chain)
	if err != nil {
		return nil, err
	}
	response := n.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var status NearStatus
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// NearSyncingValidator checks the node's own sync state via `status`:
// the explicit syncing flag plus a stale-head guard.
type NearSyncingValidator struct {
	nearStatusClient
	upstreamId string
}

func NewNearSyncingValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *NearSyncingValidator {
	return &NearSyncingValidator{
		nearStatusClient: nearStatusClient{connector: connector, chain: chain, internalTimeout: internalTimeout},
		upstreamId:       upstreamId,
	}
}

func (n *NearSyncingValidator) Validate() protocol.AvailabilityStatus {
	status, err := n.fetchNearStatus()
	if err != nil {
		log.Error().Err(err).Msgf("near upstream '%s' syncing validation failed", n.upstreamId)
		return protocol.Unavailable
	}
	if status.SyncInfo.Syncing {
		log.Warn().Msgf("near upstream '%s' is syncing", n.upstreamId)
		return protocol.Syncing
	}
	// stale-head guard: syncing=false with a frozen head is still not serviceable
	maxHeadAge := n.chain.Settings.ExpectedBlockTime * time.Duration(n.chain.Settings.Lags.Syncing)
	if maxHeadAge > 0 {
		if latestBlockTime, parseErr := time.Parse(time.RFC3339Nano, status.SyncInfo.LatestBlockTime); parseErr == nil {
			if headAge := time.Since(latestBlockTime); headAge > maxHeadAge {
				log.Warn().Msgf(
					"near upstream '%s' head is stale, latest_block_time=%s age=%s",
					n.upstreamId,
					status.SyncInfo.LatestBlockTime,
					headAge,
				)
				return protocol.Syncing
			}
		}
	}
	return protocol.Available
}

type NearStatus struct {
	ChainId  string       `json:"chain_id"`
	Version  NearVersion  `json:"version"`
	SyncInfo NearSyncInfo `json:"sync_info"`
}

type NearVersion struct {
	Version string `json:"version"`
}

type NearSyncInfo struct {
	LatestBlockHeight   uint64 `json:"latest_block_height"`
	LatestBlockTime     string `json:"latest_block_time"`
	EarliestBlockHeight uint64 `json:"earliest_block_height"`
	Syncing             bool   `json:"syncing"`
}

var _ validations.HealthValidator = (*NearSyncingValidator)(nil)
