package tron_validations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// TRON produces a block every ~3s, so block timestamps (in ms) advance by
// roughly tronBlockIntervalMs between consecutive blocks. Used to project
// how many blocks the upstream "should" have produced since blockTime.
const tronBlockIntervalMs int64 = 3000

type TronPeersValidator struct {
	upstreamId string
	chain      chains.Chain
	connector  connectors.ApiConnector
	options    *chains.Options
}

func NewTronPeersValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	options *chains.Options,
) *TronPeersValidator {
	return &TronPeersValidator{
		upstreamId: upstreamId,
		chain:      chain,
		connector:  connector,
		options:    options,
	}
}

func (t *TronPeersValidator) Validate() protocol.AvailabilityStatus {
	resp, err := t.fetchNodeInfo()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get node info of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}

	var parsed struct {
		PeerList []json.RawMessage `json:"peerList"`
	}
	if err := sonic.Unmarshal(resp, &parsed); err != nil {
		log.Error().
			Err(err).
			Msgf("unable to unmarshal node info of upstream '%s', response - %s", t.upstreamId, string(resp))
		return protocol.Unavailable
	}

	if int64(len(parsed.PeerList)) < t.options.MinPeers {
		return protocol.Immature
	}
	return protocol.Available
}

func (t *TronPeersValidator) fetchNodeInfo() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.options.InternalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("POST", "/wallet/getnodeinfo", t.chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

type TronSyncingValidator struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewTronSyncingValidator(
	upstreamId string,
	chain *chains.ConfiguredChain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *TronSyncingValidator {
	return &TronSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (t *TronSyncingValidator) Validate() protocol.AvailabilityStatus {
	resp, err := t.fetchLatestBlock()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get latest block of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}

	var parsed struct {
		BlockHeader struct {
			RawData struct {
				Number    int64 `json:"number"`
				Timestamp int64 `json:"timestamp"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := sonic.Unmarshal(resp, &parsed); err != nil {
		log.Error().
			Err(err).
			Msgf("unable to unmarshal latest block of upstream '%s', response - %s", t.upstreamId, string(resp))
		return protocol.Unavailable
	}

	currentNum := parsed.BlockHeader.RawData.Number
	blockTime := parsed.BlockHeader.RawData.Timestamp

	expectedAhead := int64(0)
	if drift := time.Now().UnixMilli() - blockTime; drift > 0 {
		expectedAhead = drift / tronBlockIntervalMs
	}
	possibleHighest := currentNum + expectedAhead

	if possibleHighest-currentNum > t.chain.Settings.Lags.Syncing {
		return protocol.Syncing
	}
	return protocol.Available
}

func (t *TronSyncingValidator) fetchLatestBlock() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("POST", "/wallet/getblock", t.chain.Chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

var _ validations.HealthValidator = (*TronPeersValidator)(nil)
var _ validations.HealthValidator = (*TronSyncingValidator)(nil)
