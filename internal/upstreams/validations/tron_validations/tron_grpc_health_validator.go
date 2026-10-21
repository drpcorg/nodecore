package tron_validations

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// TronGrpcPeersValidator is the gRPC twin of TronPeersValidator - the same
// verdict read from protocol.Wallet/GetNodeInfo's peerInfoList.
type TronGrpcPeersValidator struct {
	upstreamId string
	chain      chains.Chain
	connector  connectors.ApiConnector
	options    *chains.Options
}

func NewTronGrpcPeersValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	options *chains.Options,
) *TronGrpcPeersValidator {
	return &TronGrpcPeersValidator{
		upstreamId: upstreamId,
		chain:      chain,
		connector:  connector,
		options:    options,
	}
}

func (t *TronGrpcPeersValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), t.options.InternalTimeout)
	defer cancel()

	nodeInfo, err := specific_helpers.FetchTronGrpcNodeInfo(ctx, t.connector, t.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get node info of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}
	if int64(len(nodeInfo.GetPeerInfoList())) < t.options.MinPeers {
		return protocol.Immature
	}
	return protocol.Available
}

// TronGrpcSyncingValidator is the gRPC twin of TronSyncingValidator - the
// head's timestamp from protocol.Wallet/GetBlock (detail false) projected against the
// chain's syncing lag.
type TronGrpcSyncingValidator struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewTronGrpcSyncingValidator(
	upstreamId string,
	chain *chains.ConfiguredChain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *TronGrpcSyncingValidator {
	return &TronGrpcSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (t *TronGrpcSyncingValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), t.internalTimeout)
	defer cancel()

	raw, err := specific_helpers.FetchTronGrpcNowBlock(ctx, t.connector, t.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get latest block of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}
	block, err := specific_helpers.ParseTronGrpcBlock(raw)
	if err != nil {
		log.Error().Err(err).Msgf("unable to unmarshal latest block of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}
	header := block.GetBlockHeader().GetRawData()
	if header == nil {
		log.Error().Msgf("latest block of upstream '%s' has no header", t.upstreamId)
		return protocol.Unavailable
	}
	return specific_helpers.TronSyncStatus(header.GetTimestamp(), time.Now().UnixMilli(), t.chain.Settings.Lags.Syncing)
}

var _ validations.HealthValidator = (*TronGrpcPeersValidator)(nil)
var _ validations.HealthValidator = (*TronGrpcSyncingValidator)(nil)
