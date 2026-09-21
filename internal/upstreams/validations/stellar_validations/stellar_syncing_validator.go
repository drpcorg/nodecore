package stellar_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const stellarHealthyStatus = "healthy"

// While its data stores bootstrap, stellar-rpc rejects getHealth with a -32603
// carrying this text. Every other rejection - including its own >30s staleness
// verdict - means the upstream is unusable rather than catching up.
const stellarNotInitializedMarker = "not initialized"

type StellarSyncingValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStellarSyncingValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StellarSyncingValidator {
	return &StellarSyncingValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarSyncingValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	health, err := specific_helpers.FetchStellarHealth(ctx, s.connector, s.chain.Chain)
	if err != nil {
		if strings.Contains(err.Error(), stellarNotInitializedMarker) {
			log.Warn().Msgf("stellar upstream '%s' is bootstrapping its data stores", s.upstreamId)
			return protocol.Syncing
		}
		log.Error().Err(err).Msgf("stellar upstream '%s' syncing validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	if health.Status != stellarHealthyStatus {
		log.Warn().Msgf("stellar upstream '%s' reports status '%s'", s.upstreamId, health.Status)
		return protocol.Unavailable
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*StellarSyncingValidator)(nil)
