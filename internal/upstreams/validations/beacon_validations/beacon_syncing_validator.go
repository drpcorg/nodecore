package beacon_validations

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

var (
	errBeaconSyncing   = errors.New("beacon node is still syncing")
	errBeaconElOffline = errors.New("beacon node reports its execution layer is offline")
)

// BeaconChainSyncingValidator probes GET /eth/v1/node/syncing. The upstream is:
//   - Unavailable when the request fails or the execution layer is offline;
//   - Syncing when is_syncing is true;
//   - Available otherwise.
type BeaconChainSyncingValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewBeaconChainSyncingValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *BeaconChainSyncingValidator {
	return &BeaconChainSyncingValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (b *BeaconChainSyncingValidator) Validate() protocol.AvailabilityStatus {
	syncing, err := b.fetchSyncing()
	if err != nil {
		log.Error().Err(err).Msgf("beacon upstream '%s' syncing validation failed", b.upstreamId)
		return protocol.Unavailable
	}
	if syncing.Data.ElOffline {
		log.Error().Err(errBeaconElOffline).Msgf("beacon upstream '%s' execution layer is offline", b.upstreamId)
		return protocol.Unavailable
	}
	if syncing.Data.IsSyncing {
		log.Warn().Err(errBeaconSyncing).Msgf("beacon upstream '%s' is syncing", b.upstreamId)
		return protocol.Syncing
	}
	return protocol.Available
}

func (b *BeaconChainSyncingValidator) fetchSyncing() (*beaconChainSyncing, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/node/syncing", nil, b.chain)

	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var syncing beaconChainSyncing
	if err := sonic.Unmarshal(response.ResponseResult(), &syncing); err != nil {
		return nil, err
	}
	return &syncing, nil
}

type beaconChainSyncing struct {
	Data struct {
		IsSyncing bool `json:"is_syncing"`
		ElOffline bool `json:"el_offline"`
	} `json:"data"`
}

var _ validations.HealthValidator = (*BeaconChainSyncingValidator)(nil)
