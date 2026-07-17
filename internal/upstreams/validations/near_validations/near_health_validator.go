package near_validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errNearNoPeers = errors.New("near node has no active peers")

type NearHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	validatePeers   bool
}

func NewNearHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	validatePeers bool,
) *NearHealthValidator {
	return &NearHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
		validatePeers:   validatePeers,
	}
}

func (n *NearHealthValidator) Validate() protocol.AvailabilityStatus {
	status, err := fetchNearStatus(n.connector, n.chain.Chain, n.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("near upstream '%s' health validation failed", n.upstreamId)
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
	if n.validatePeers {
		peers, err := n.fetchActivePeers()
		if err != nil {
			log.Error().Err(err).Msgf("failed to fetch near network info for upstream '%s'", n.upstreamId)
			return protocol.Unavailable
		}
		if peers == 0 {
			log.Error().Err(errNearNoPeers).Msgf("near upstream '%s' has no active peers", n.upstreamId)
			return protocol.Unavailable
		}
	}
	return protocol.Available
}

func (n *NearHealthValidator) fetchActivePeers() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), n.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("network_info", []any{}, n.chain.Chain)
	if err != nil {
		return 0, err
	}
	response := n.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	var info nearNetworkInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return 0, err
	}
	return info.NumActivePeers, nil
}

func fetchNearStatus(connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*NearStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("status", []any{}, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var status NearStatus
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		return nil, err
	}
	return &status, nil
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

type nearNetworkInfo struct {
	NumActivePeers uint64 `json:"num_active_peers"`
}

var _ validations.HealthValidator = (*NearHealthValidator)(nil)
