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

type CosmosSyncingValidator struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewCosmosSyncingValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *CosmosSyncingValidator {
	return &CosmosSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (c *CosmosSyncingValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	syncing, err := specific_helpers.FetchCosmosSyncing(ctx, c.connector, c.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get the syncing state of upstream '%s'", c.upstreamId)
		return protocol.Unavailable
	}
	if syncing {
		return protocol.Syncing
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*CosmosSyncingValidator)(nil)
