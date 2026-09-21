package cosmos_validations

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

// CosmosGrpcSyncingValidator is the gRPC twin of CosmosSyncingValidator - the
// same verdicts read from cosmos.base.tendermint.v1beta1.Service/GetSyncing.
type CosmosGrpcSyncingValidator struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewCosmosGrpcSyncingValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *CosmosGrpcSyncingValidator {
	return &CosmosGrpcSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (c *CosmosGrpcSyncingValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	syncing, err := specific_helpers.FetchCosmosGrpcSyncing(ctx, c.connector, c.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get the syncing state of upstream '%s'", c.upstreamId)
		return protocol.Unavailable
	}
	if syncing {
		return protocol.Syncing
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*CosmosGrpcSyncingValidator)(nil)
