package tendermint_validations

import (
	"context"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type TendermintSyncingValidator struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewTendermintSyncingValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *TendermintSyncingValidator {
	return &TendermintSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (t *TendermintSyncingValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), t.internalTimeout)
	defer cancel()

	status, err := specific_helpers.FetchTendermintStatus(ctx, t.connector, t.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get the status of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}
	if status.SyncInfo.CatchingUp {
		return protocol.Syncing
	}
	return protocol.Available
}

type TendermintPeersValidator struct {
	upstreamId string
	chain      chains.Chain
	connector  connectors.ApiConnector
	options    *chains.Options
}

func NewTendermintPeersValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	options *chains.Options,
) *TendermintPeersValidator {
	return &TendermintPeersValidator{
		upstreamId: upstreamId,
		chain:      chain,
		connector:  connector,
		options:    options,
	}
}

func (t *TendermintPeersValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), t.options.InternalTimeout)
	defer cancel()

	raw, err := specific_helpers.TendermintCall(
		ctx, t.connector, t.chain, "net_info", nil,
	)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get net_info of upstream '%s'", t.upstreamId)
		return protocol.Unavailable
	}

	var parsed struct {
		NPeers string `json:"n_peers"`
	}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		log.Error().
			Err(err).
			Msgf("unable to unmarshal net_info of upstream '%s', response - %s", t.upstreamId, string(raw))
		return protocol.Unavailable
	}
	peers, err := strconv.ParseInt(parsed.NPeers, 10, 64)
	if err != nil {
		log.Error().
			Err(err).
			Msgf("unable to parse n_peers of upstream '%s', response - %s", t.upstreamId, string(raw))
		return protocol.Unavailable
	}

	if peers < t.options.MinPeers {
		return protocol.Immature
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*TendermintSyncingValidator)(nil)
var _ validations.HealthValidator = (*TendermintPeersValidator)(nil)
