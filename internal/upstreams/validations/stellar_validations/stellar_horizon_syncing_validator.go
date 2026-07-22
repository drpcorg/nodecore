package stellar_validations

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type StellarHorizonSyncingValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStellarHorizonSyncingValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StellarHorizonSyncingValidator {
	return &StellarHorizonSyncingValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarHorizonSyncingValidator) Validate() protocol.AvailabilityStatus {
	health, err := FetchStellarHorizonHealth(s.connector, s.chain.Chain, s.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("horizon upstream '%s' syncing validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	if health.DatabaseConnected && health.CoreUp && health.CoreSynced {
		return protocol.Available
	}
	// the node itself is fine but its captive core has not caught up yet
	if health.DatabaseConnected && health.CoreUp {
		log.Warn().Msgf("horizon upstream '%s' reports core_synced=false", s.upstreamId)
		return protocol.Syncing
	}
	log.Warn().Msgf(
		"horizon upstream '%s' is unhealthy: database_connected=%t core_up=%t core_synced=%t",
		s.upstreamId, health.DatabaseConnected, health.CoreUp, health.CoreSynced,
	)
	return protocol.Unavailable
}

var _ validations.HealthValidator = (*StellarHorizonSyncingValidator)(nil)
